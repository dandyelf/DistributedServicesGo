package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"21school/warehouse/internal/common"
	"21school/warehouse/internal/storage"
	"21school/warehouse/types"
)

const (
	numberOfTries = 3
	// in ms
	deadInterval = 200
)

type Logic interface {
	ServerLogic() error
	Hello() error
}

type Server struct {
	addresses    []string
	addr         string
	storage      storage.Storage
	leader       bool
	neighbor     string
	replicFactor int
	client       *http.Client
	infoLog      *log.Logger
	errorLog     *log.Logger
}

func main() {
	infoLog := log.New(os.Stdout, "INFO: ", log.Ldate|log.Ltime)
	errorLog := log.New(os.Stderr, "ERROR: ", log.Ldate|log.Ltime|log.Lshortfile)
	s := Server{
		storage:  storage.NewCache(),
		infoLog:  infoLog,
		errorLog: errorLog,
	}

	flag.BoolVar(&s.leader, "L", false, "start as leader server")
	flag.StringVar(&s.addr, "A", "127.0.0.1:8888", "ip_address:port")
	flag.StringVar(&s.neighbor, "N", "", "neighbor instance to connect")
	flag.IntVar(&s.replicFactor, "RF", 2, "replication factor")
	flag.Parse()
	s.addresses = []string{s.addr}
	if s.neighbor == "" && !s.leader {
		s.errorLog.Fatal("start with -L option or point to neighbor instance with -N")
	}
	start(&s)
}

func start(s Logic) error {
	err := s.Hello()
	if err != nil {
		log.Fatal(err)
	}
	err = s.ServerLogic()
	if err != nil {
		log.Fatal(err)
	}
	return nil
}

func (s *Server) ServerLogic() error {
	http.HandleFunc("/api/heartbeat", s.heartbeat)
	http.HandleFunc("/api/hello", s.helloServersResponse)
	http.HandleFunc("/api/addnode", s.addNode)
	http.HandleFunc("GET /api/items/", s.middleware(s.middlewareGet(s.get)))
	http.HandleFunc("POST /api/items/", s.middleware(s.middlewareSet(s.set)))
	http.HandleFunc("DELETE /api/items/", s.middleware(s.middlewareDel(s.del)))
	s.infoLog.Print("Server address: ", s.addr)
	go s.healthCheck()
	return http.ListenAndServe(s.addr, nil)
}

func (s *Server) Hello() error {
	s.client = &http.Client{
		Timeout: 1 * time.Second,
	}
	if !s.leader {
		return s.helloServersRequest()
	}
	return nil
}

func (s *Server) findLeader() {
	if len(s.addresses) < 2 {
		s.leader = true
		return
	}
	var mu sync.RWMutex
	mu.Lock()
	slices.SortFunc(s.addresses, func(a, b string) int {
		return -strings.Compare(strings.ToLower(a), strings.ToLower(b))
	})
	mu.Unlock()

	if s.addr == s.addresses[0] {
		s.infoLog.Print("I'm Leader")
		s.leader = true
	} else {
		s.leader = false
	}
}

func parseReqURL(parsedURL string, addr string) (string, error) {
	u, err := url.Parse(parsedURL)
	if err != nil {
		return "", err
	}
	u.Scheme = "http"
	u.Host = addr
	return u.String(), nil
}

func (s *Server) middleware(next http.HandlerFunc) http.HandlerFunc {
	return http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			id := r.URL.Query().Get("uuid")
			if id == "" {
				s.errorLog.Print("no uuid provided")
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			next(w, r)
		})
}

func (s *Server) sendProxyReq(r *http.Request, addr string, rdr io.ReadCloser) (*http.Response, error) {
	newURL, err := parseReqURL(r.URL.String(), addr)
	if err != nil {
		return nil, fmt.Errorf("can't parse url: %v, err: %w", r.URL.String(), err)
	}

	proxyReq, err := http.NewRequest(r.Method, newURL, rdr)
	if err != nil {
		return nil, fmt.Errorf("error creating proxy request: %w", err)
	}

	for name, values := range r.Header {
		for _, value := range values {
			proxyReq.Header.Add(name, value)
		}
	}

	res, err := s.client.Do(proxyReq)
	if err != nil {
		return nil, fmt.Errorf("error sending proxy request: %w", err)
	}
	return res, nil
}

func (s *Server) middlewareSet(next http.HandlerFunc) http.HandlerFunc {
	return http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if len(s.addresses) < 2 || !s.leader {
				next(w, r)
				return
			}

			id := r.URL.Query().Get("uuid")
			addrs := append([]string(nil), s.addresses...)

			f := s.replicFactor
			if f > len(addrs) {
				f = len(addrs)
			}

			addrList := common.HashFunction(&addrs, f, id)
			ch := make(chan bool)
			var wg sync.WaitGroup

			buf, _ := io.ReadAll(r.Body)

			for _, addr := range *addrList {
				wg.Add(1)
				go func() {
					rdr := io.NopCloser(bytes.NewBuffer(buf))
					defer wg.Done()
					if addr == s.addr {
						var data types.Req
						err := json.NewDecoder(rdr).Decode(&data)
						if err != nil {
							s.errorLog.Printf("can't decode: %v, err: %v", rdr, err)
							return
						}

						err = s.storage.Set(id, data.Name)
						if err != nil {
							s.errorLog.Printf("can't save: %v, err: %v", id, err)
							return
						}

						s.infoLog.Printf("added id: %v, with value: %v", id, data.Name)
						ch <- true
						return
					}

					res, err := s.sendProxyReq(r, addr, rdr)
					if err != nil {
						s.errorLog.Print(err)
					}
					defer res.Body.Close()

					if res.StatusCode == http.StatusOK {
						ch <- true
						return
					}
				}()
			}

			go func() {
				defer close(ch)
				wg.Wait()
			}()

			var cnt int
			for _ = range ch {
				cnt++
			}

			if cnt > 0 {
				s.infoLog.Printf("Created %v replices", cnt)
				data := types.Resp{
					Replicas: cnt,
				}
				w.Header().Set("Content-type", "application/json")
				w.WriteHeader(http.StatusOK)
				err := json.NewEncoder(w).Encode(data)
				if err != nil {
					s.errorLog.Print(err)
				}
			} else {
				w.WriteHeader(http.StatusBadRequest)
			}
		})
}

func (s *Server) middlewareGet(next http.HandlerFunc) http.HandlerFunc {
	return http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if len(s.addresses) < 2 || !s.leader {
				next(w, r)
				return
			}

			id := r.URL.Query().Get("uuid")
			addrs := append([]string(nil), s.addresses...)

			ch := make(chan string)
			var wg sync.WaitGroup

			buf, _ := io.ReadAll(r.Body)

			for _, addr := range addrs {
				wg.Add(1)
				go func() {
					rdr := io.NopCloser(bytes.NewBuffer(buf))
					defer wg.Done()
					if addr == s.addr {
						name, err := s.storage.Get(id)
						if err != nil {
							return
						}

						ch <- name
						return
					}

					res, err := s.sendProxyReq(r, addr, rdr)
					if err != nil {
						s.errorLog.Print(err)
					}
					defer res.Body.Close()

					if res.StatusCode == http.StatusOK {
						var data types.Req
						err := json.NewDecoder(res.Body).Decode(&data)
						if err != nil {
							return
						}

						ch <- data.Name
					}
				}()
			}

			go func() {
				defer close(ch)
				wg.Wait()
			}()

			var n string
			for name := range ch {
				n = name
			}

			if n != "" {
				data := types.Req{
					Name: n,
				}

				w.Header().Set("Content-type", "application/json")
				s.infoLog.Printf("requested: %v, data: %v", id, data.Name)
				w.WriteHeader(http.StatusOK)
				err := json.NewEncoder(w).Encode(data)
				if err != nil {
					s.errorLog.Print(err)
				}
				return
			}

			http.NotFound(w, r)
		})
}

func (s *Server) middlewareDel(next http.HandlerFunc) http.HandlerFunc {
	return http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if len(s.addresses) < 2 || !s.leader {
				next(w, r)
				return
			}

			id := r.URL.Query().Get("uuid")
			addrs := append([]string(nil), s.addresses...)

			ch := make(chan bool)
			var wg sync.WaitGroup

			buf, _ := io.ReadAll(r.Body)

			for _, addr := range addrs {
				wg.Add(1)

				go func() {
					rdr := io.NopCloser(bytes.NewBuffer(buf))
					defer wg.Done()
					if addr == s.addr {
						err := s.storage.Delete(id)
						if err != nil {
							return
						}

						ch <- true
						s.infoLog.Printf("deleted: %v", id)
						return
					}

					res, err := s.sendProxyReq(r, addr, rdr)
					if err != nil {
						s.errorLog.Print(err)
					}
					defer res.Body.Close()

					if res.StatusCode == http.StatusOK {
						ch <- true
					}
				}()
			}

			go func() {
				defer close(ch)
				wg.Wait()
			}()

			var cnt int
			for _ = range ch {
				cnt++
			}

			if cnt > 0 {
				s.infoLog.Printf("Deleted %v replices", cnt)
				data := types.Resp{
					Replicas: cnt,
				}
				w.Header().Set("Content-type", "application/json")
				w.WriteHeader(http.StatusOK)
				err := json.NewEncoder(w).Encode(data)
				if err != nil {
					s.errorLog.Print(err)
				}
			} else {
				w.WriteHeader(http.StatusBadRequest)
			}
		})
}

func (s *Server) set(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("uuid")
	var reqData types.Req
	err := json.NewDecoder(r.Body).Decode(&reqData)
	if err != nil {
		s.errorLog.Printf("can't decode: %v, err: %v", r.Body, err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	err = s.storage.Set(id, reqData.Name)
	if err != nil {
		s.errorLog.Printf("can't save: %v, err: %v", id, err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	s.infoLog.Printf("added id: %v, with value: %v", id, reqData.Name)

	respData := types.Resp{
		Replicas: 1,
	}

	w.Header().Set("Content-type", "application/json")
	w.WriteHeader(http.StatusOK)
	err = json.NewEncoder(w).Encode(respData)
	if err != nil {
		s.errorLog.Print(err)
	}
}

func (s *Server) get(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("uuid")

	name, err := s.storage.Get(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	data := types.Req{
		Name: name,
	}

	w.Header().Set("Content-type", "application/json")
	s.infoLog.Print("requested: ", id, ":", data.Name)
	w.WriteHeader(http.StatusOK)
	err = json.NewEncoder(w).Encode(data)
	if err != nil {
		s.errorLog.Print(err)
	}
}

func (s *Server) del(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("uuid")

	err := s.storage.Delete(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	data := types.Resp{
		Replicas: 1,
	}

	w.Header().Set("Content-type", "application/json")
	s.infoLog.Printf("deleted: %v", id)
	w.WriteHeader(http.StatusOK)
	err = json.NewEncoder(w).Encode(data)
	if err != nil {
		s.errorLog.Print(err)
	}
}

func (s *Server) heartbeat(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-type", "application/json")
	w.WriteHeader(http.StatusOK)
	data := types.JSONHeartbeat{
		Addresses:    s.addresses,
		ReplicFactor: s.replicFactor,
	}
	json.NewEncoder(w).Encode(data)
}

func (s *Server) candidateExist(addr string) bool {
	if len(s.addresses) > 2 {
		for _, node := range s.addresses {
			if node == addr {
				return true
			}
		}
	}
	return false
}

func (s *Server) nodeIsAlive(node string) bool {
	req, err := http.NewRequest(http.MethodGet, "http://"+node+"/api/heartbeat", nil)
	if err != nil {
		s.errorLog.Print(err)
		return false
	}
	res, err := s.client.Do(req)
	if err != nil {
		s.errorLog.Print(err)
		return false
	}
	defer res.Body.Close()
	if h := res.Header.Get("Content-type"); h == "" || res.StatusCode != http.StatusOK {
		return false
	}
	return true
}

func (s *Server) healthCheck() {
	for {
		time.Sleep(time.Second)
		if len(s.addresses) < 2 {
			continue
		}

		ch := make(chan string)
		var wg sync.WaitGroup
		defer close(ch)
		copyAddresses := append([]string(nil), s.addresses...)
		for _, node := range copyAddresses {
			if node == s.addr {
				continue
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				var isAlive bool
				for range numberOfTries {
					if s.nodeIsAlive(node) {
						isAlive = true
						return
					}
					time.Sleep(time.Millisecond * deadInterval)
				}
				if !isAlive {
					ch <- node
				}
			}()
		}

		go func() {
			defer close(ch)
			wg.Wait()
		}()

		nodesToRemove := make([]string, 0)
		for node := range ch {
			nodesToRemove = append(nodesToRemove, node)
		}
		if len(nodesToRemove) > 0 {
			s.infoLog.Print("We lost them: ", nodesToRemove)
			s.removeLostNodes(nodesToRemove)
			s.findLeader()
			s.infoLog.Print("Nodes: ", s.addresses)
			s.infoLog.Print("Leader: ", s.addresses[0])
		} else {
			//s.infoLog.Print("all nodes are alive")
		}
	}
}

func (s *Server) removeLostNodes(nodeToRemove []string) {
	copyAddresses := append([]string(nil), s.addresses...)
	for _, node := range nodeToRemove {
		for i := 0; i < len(copyAddresses); i++ {
			if node == copyAddresses[i] {
				copyAddresses = append(copyAddresses[:i], copyAddresses[i+1:]...)
				i--
				break
			}
		}
	}
	var mu sync.RWMutex
	mu.Lock()
	defer mu.Unlock()
	s.addresses = copyAddresses
}

func (s *Server) notify(addr string) error {
	var err error
	if len(s.addresses) < 2 {
		return fmt.Errorf("there are no servers to notify")
	}

	data := types.JSONHelloReq{
		Address: addr,
	}
	reqData, err := json.Marshal(data)
	if err != nil {
		return err
	}

	for _, node := range s.addresses {
		if node == s.addr {
			continue
		}
		req, err := http.NewRequest(http.MethodPost, "http://"+node+"/api/addnode", bytes.NewReader(reqData))
		if err != nil {
			continue
		}

		req.Header.Add("Content-type", "application/json")
		req.Header.Add("Notify-packet", addr)
		res, err := s.client.Do(req)
		if err != nil {
			// TODO check status of this server and remove from s.addresses if fail
			continue
		}
		defer res.Body.Close()
	}
	return err
}

func (s *Server) addNode(w http.ResponseWriter, r *http.Request) {
	if h := r.Header.Get("Notify-packet"); h == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	var reqData types.JSONHelloReq
	err := json.NewDecoder(r.Body).Decode(&reqData)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	if !s.candidateExist(reqData.Address) {
		s.addresses = append(s.addresses, reqData.Address)
		s.findLeader()
		s.infoLog.Print("New node: ", reqData.Address)
		s.infoLog.Print("Nodes: ", s.addresses)
		s.infoLog.Print("Leader: ", s.addresses[0])
	}
}

func (s *Server) helloServersResponse(w http.ResponseWriter, r *http.Request) {
	if h := r.Header.Get("Hello-packet"); h == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	var reqData types.JSONHelloReq
	err := json.NewDecoder(r.Body).Decode(&reqData)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if reqData.ReplicFactor != s.replicFactor {
		w.Header().Set("Replication-factor", fmt.Sprintf("%v", s.replicFactor))
		w.WriteHeader(http.StatusNotAcceptable)
		return
	}

	w.Header().Set("Content-type", "application/json")

	respData := storage.JSONHelloResp{
		Addresses:    s.addresses,
		ReplicFactor: s.replicFactor,
	}
	err = json.NewEncoder(w).Encode(&respData)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if !s.candidateExist(reqData.Address) {
		err = s.notify(reqData.Address)
		if err != nil {
			s.errorLog.Print(err)
		}
		s.addresses = append(s.addresses, reqData.Address)
		s.findLeader()
		s.infoLog.Print("New node: ", reqData.Address)
		s.infoLog.Print("Nodes: ", s.addresses)
		s.infoLog.Print("Leader: ", s.addresses[0])
	}
}

func (s *Server) helloServersRequest() error {
	data := types.JSONHelloReq{
		Address:      s.addr,
		ReplicFactor: s.replicFactor,
	}
	reqData, err := json.Marshal(data)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, "http://"+s.neighbor+"/api/hello", bytes.NewReader(reqData))
	if err != nil {
		return err
	}
	req.Header.Add("Content-type", "application/json")
	req.Header.Add("Hello-packet", s.addr)
	res, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.StatusCode == http.StatusNotAcceptable {
		return fmt.Errorf("replication factor mismatch: my rf %v, servers rf %v", s.replicFactor, res.Header.Get("Replication-factor"))
	}

	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status: got %v", res.Status)
	}

	var respData storage.JSONHelloResp
	err = json.NewDecoder(res.Body).Decode(&respData)
	if err != nil {
		return err
	}
	// NB to check s.addresses
	s.addresses = append(s.addresses, respData.Addresses...)
	s.findLeader()
	s.infoLog.Print("Nodes: ", s.addresses)
	s.infoLog.Print("Leader: ", s.addresses[0])

	return nil
}
