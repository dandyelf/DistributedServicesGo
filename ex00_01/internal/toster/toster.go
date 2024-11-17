package toster

import (
	"21school/warehouse/internal/postparser"
	"21school/warehouse/types"
	"bytes"
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

type Toster struct {
	addresses        []string
	addr             string
	proto            string
	replfactor       int
	connectedServers int64
	client           *http.Client
	postretr         int
	itemapi          string
	heartbitapi      string
}

func (p *Toster) Connect(addrress string) error {
	p.proto = "http://"
	p.itemapi = "/api/items/"
	p.heartbitapi = "/api/heartbeat"
	p.addr = addrress
	p.postretr = 5
	p.client = &http.Client{}
	return p.connect()
}

func (p *Toster) connect() error {
	data, err := p.heartbeat()
	if err != nil {

		return err
	}
	p.addresses = data.Addresses
	p.replfactor = data.ReplicFactor
	p.connectedServers = int64(len(data.Addresses))
	log.Println("Known nodes:", data.Addresses)
	log.Println("ReplicFactor:", data.ReplicFactor)
	return nil
}

func (p *Toster) heartbeat() (*types.JSONHeartbeat, error) {
	resp, err := http.Get(p.proto + p.addr + p.heartbitapi)
	if err != nil {

		return nil, err
	}
	defer resp.Body.Close()
	var data types.JSONHeartbeat
	err = json.NewDecoder(resp.Body).Decode(&data)
	if err != nil {
		log.Println(err, ">")

		return nil, err
	}
	return &data, nil
}

func (p *Toster) UpdateIPs() {
	warning := false
	for len(p.addresses) != 0 {
		data, err := p.heartbeat()
		if err != nil {
			p.rotateIP()
			continue
		} else if len(data.Addresses) < data.ReplicFactor && !warning {
			log.Println("Known nodes: \n", data.Addresses)
			log.Printf("WARNING: cluster size (%d) is smaller than a replication factor (%d)!", len(data.Addresses), data.ReplicFactor)
			log.Println(">")
			warning = true
		} else if len(data.Addresses) >= data.ReplicFactor {
			warning = false
		}
		var mu sync.RWMutex
		mu.Lock()
		defer mu.Unlock()
		p.addresses = data.Addresses
		p.replfactor = data.ReplicFactor
		atomic.StoreInt64(&p.connectedServers, int64(len(p.addresses)))
		time.Sleep(1 * time.Second)
	}
	atomic.StoreInt64(&p.connectedServers, int64(len(p.addresses)))
	time.Sleep(1 * time.Second)
	log.Println("no servers available", ">")
}

func (p *Toster) rotateIP() {
	atomic.StoreInt64(&p.connectedServers, 0)
	p.addr = p.addresses[0]
	if len(p.addresses) != 1 {
		p.addresses = p.addresses[:len(p.addresses)-1]
	} else {
		p.addresses = []string{}
	}
	fmt.Println("Reconnected to a database of Warehouse at: ", p.addr)
	fmt.Println("Known nodes:", p.addresses)
	fmt.Println(">")
}

func (p *Toster) Post(str string) (string, error) {
	req, err := postparser.Postcreator(str)

	if err != nil {
		fmt.Println(err)
		return err.Error(), err
	}
	switch req.Method {
	case "GET":
		get, err := p.getCycle(req)
		if err != nil {
			return "", err
		}
		if !get.Status {
			return "not found", nil
		}
		b, err := json.Marshal(get.Data)
		if err != nil {
			return "", err
		}
		return string(b), nil
	case "DELETE":
		get, err := p.delCycle(req)
		if get > 0 {
			return fmt.Sprintf("Deleted (%d replicas)", get), nil
		}
		if err != nil {
			return "", err
		}
		return "not found", nil
	case "POST":
		posts, err := p.postCycle(req)
		if posts > 0 {
			return fmt.Sprintf("Created (%d replicas)", posts), nil
		}
		return "", err
	}

	return "", nil
}

func (p *Toster) getCycle(req types.PosterReq) (*types.PosterResp, error) {
	retries := p.postretr
	var wg sync.WaitGroup
	for retries > 0 {
		if p.connectedServers < 0 {
			time.Sleep(2 * time.Second)
			retries--

			continue
		}
		adrstr := p.addresses
		wg.Add(len(adrstr))
		var list []types.PosterResp = make([]types.PosterResp, len(adrstr))

		for n, addr := range adrstr {
			req.Addr = p.proto + addr + p.itemapi
			go p.getReq(&wg, req, &list[n])
		}
		wg.Wait()
		answer, err := p.getCheck(&list)
		if err != nil {
			retries--
			time.Sleep(1 * time.Second)
			continue
		} else {
			return answer, nil
		}
	}
	return nil, fmt.Errorf("not found")
}

func (p *Toster) getCheck(list *[]types.PosterResp) (*types.PosterResp, error) {
	if list == nil {

		return nil, fmt.Errorf("list = nil")
	}
	for _, v := range *list {
		if v.Status {

			return &v, nil
		}
	}
	errcount := 0
	for _, v := range *list {
		if v.Err != nil {

			errcount++
		}
	}

	if len(*list) == errcount {
		return nil, fmt.Errorf("getCheck no answers")
	}
	var notfound types.PosterResp
	return &notfound, nil
}

func (p *Toster) getReq(wg *sync.WaitGroup, req types.PosterReq, res *types.PosterResp) {
	if wg == nil {
		log.Println("wg nil", ">")

		return
	}
	defer wg.Done()
	if res == nil {
		log.Println("res nil", ">")

		return
	}
	r, err := http.NewRequest(req.Method, req.Addr, nil)
	if err != nil {
		res.Err = err

		return
	}
	q := r.URL.Query()
	for k, v := range req.Formquery {
		q.Add(k, v)
	}
	r.URL.RawQuery = q.Encode()
	r.Header.Add("Content-Type", "application/json")
	resp, err := p.client.Do(r)
	if err != nil {
		res.Err = err

		return
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		err = json.NewDecoder(resp.Body).Decode(&res.Data)
		if err != nil {
			res.Err = err

			return
		}
		res.Status = true
	}
}

func (p *Toster) delCycle(req types.PosterReq) (int, error) {
	retries := p.postretr
	var wg sync.WaitGroup
	for retries > 0 {
		if p.connectedServers < 0 {
			time.Sleep(2 * time.Second)
			retries--

			continue
		}
		adrstr := p.addresses
		wg.Add(len(adrstr))
		var list []types.PosterResp = make([]types.PosterResp, len(adrstr))

		for n, addr := range adrstr {
			req.Addr = p.proto + addr + p.itemapi
			go p.delReq(&wg, req, &list[n])
		}
		wg.Wait()
		answer, err := p.delCheck(&list)
		if err != nil {
			retries--
			time.Sleep(1 * time.Second)
			continue
		} else {
			return answer, nil
		}
	}
	return 0, fmt.Errorf("not found")
}

func (p *Toster) delCheck(list *[]types.PosterResp) (int, error) {
	if list == nil {
		return 0, fmt.Errorf("list = nil")
	}
	count := 0
	var err error
	for _, v := range *list {
		if v.Status {
			count++
		}
		if v.Err != nil {
			err = v.Err
		}
	}

	return count, err
}

func (p *Toster) delReq(wg *sync.WaitGroup, req types.PosterReq, res *types.PosterResp) {
	if wg == nil {
		log.Println("wg nil", ">")

		return
	}
	defer wg.Done()
	if res == nil {
		log.Println("res nil", ">")

		return
	}
	r, err := http.NewRequest(req.Method, req.Addr, nil)
	if err != nil {
		res.Err = err

		return
	}
	q := r.URL.Query()
	for k, v := range req.Formquery {
		q.Add(k, v)
	}
	r.URL.RawQuery = q.Encode()
	r.Header.Add("Content-Type", "application/json")
	resp, err := p.client.Do(r)
	if err != nil {
		res.Err = err

		return
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		res.Status = true
	}
}

func (p *Toster) postCycle(req types.PosterReq) (int, error) {
	retries := p.postretr
	for retries > 0 {
		if p.connectedServers < 0 {
			time.Sleep(2 * time.Second)
			retries--

			continue
		} else {
			break
		}
	}
	if retries == 0 {
		return 0, fmt.Errorf("not found")
	}
	f := p.replfactor
	var wg sync.WaitGroup
	addrs := make([]string, len(p.addresses))
	copy(addrs, p.addresses)
	if f > len(addrs) {
		f = len(addrs)
	}
	var answer int
	var err error
	retries = f + 5
	for f > 0 && retries > 0 {
		addrlist := p.hashFunction(&addrs, f, req.Formquery["uuid"])
		wg.Add(len(*addrlist))

		var list []types.PosterResp = make([]types.PosterResp, len(*addrlist))

		for n, addr := range *addrlist {
			req.Addr = p.proto + addr + p.itemapi
			go p.postReq(&wg, req, &list[n])
		}
		wg.Wait()
		answer, err = p.postCheck(&list)
		f = f - answer
		if len(addrs) == 0 {
			break
		}
		if f > 0 {
			time.Sleep(1 * time.Second)
		}
		retries--
	}

	return answer, err
}

func (p *Toster) hashFunction(addrs *[]string, num int, id string) *[]string {
	var s []string
	for i := 0; i < num; i++ {
		r := p.hashCalc(len(*addrs), id)
		a := (*addrs)[r]
		s = append(s, a)
		*addrs = append((*addrs)[:r], (*addrs)[r+1:]...)
	}
	return &s
}

func (p *Toster) hashCalc(n int, s string) int {
	u, err := uuid.Parse(s)
	if err != nil {
		return 0
	}
	h := sha1.New()
	h.Write(u[:])
	sum := h.Sum(nil)
	var hash uint64
	for i := 0; i < 8; i++ {
		hash <<= 8
		hash |= uint64(sum[i])
	}

	return int(hash % uint64(n))
}

func (p *Toster) postCheck(list *[]types.PosterResp) (int, error) {
	if list == nil {
		return 0, fmt.Errorf("list = nil")
	}
	counter := 0
	var err error
	for _, v := range *list {
		if v.Status {
			counter++
		}
		if v.Err != nil {
			err = v.Err
		}
	}

	return counter, err
}

func (p *Toster) postReq(wg *sync.WaitGroup, preq types.PosterReq, res *types.PosterResp) {
	if wg == nil {
		log.Println("wg nil", ">")

		return
	}
	defer wg.Done()
	if res == nil {
		log.Println("res nil", ">")

		return
	}
	payloadBuf := new(bytes.Buffer)
	json.NewEncoder(payloadBuf).Encode(preq.Req)
	r, err := http.NewRequest(preq.Method, preq.Addr, payloadBuf)
	if err != nil {
		log.Println(err)

		return
	}
	q := r.URL.Query()
	for k, v := range preq.Formquery {
		q.Add(k, v)
	}
	r.URL.RawQuery = q.Encode()
	r.Header.Add("Content-Type", "application/json")
	resp, err := p.client.Do(r)
	if err != nil {
		log.Println(err)

		return
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		res.Status = true
	}
}
