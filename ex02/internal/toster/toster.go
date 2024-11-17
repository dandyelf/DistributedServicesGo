package toster

import (
	"21school/warehouse/internal/postparser"
	"21school/warehouse/types"
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

const (
	CLI = iota
	ERROR
	INFO
	postretr    = 5
	proto       = "http://"
	itemapi     = "/api/items/"
	heartbitapi = "/api/heartbeat"
)

type Loger interface {
	Error(error, string)
	Info(string)
	UserLevel(string)
}

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
	Loger            Loger
	activeserver     int
	reconnected      bool
}

func (p *Toster) Connect(addrress string) error {
	p.proto = proto
	p.itemapi = itemapi
	p.heartbitapi = heartbitapi
	p.addr = addrress
	p.postretr = postretr
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
	p.addr = data.Addresses[0]
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
	for {
		data := p.halloCycle()
		if len(data.Addresses) < data.ReplicFactor && !warning {
			p.log(nil, "Known nodes: \n"+fmt.Sprintln(data.Addresses), INFO)
			p.log(nil, fmt.Sprintf("'WARNING: cluster size (%d) is smaller than a replication factor (%d)!'", len(data.Addresses), data.ReplicFactor), CLI)
			warning = true
		} else if len(data.Addresses) >= data.ReplicFactor {
			warning = false
		}
		p.leaderSet(data)
		atomic.StoreInt64(&p.connectedServers, int64(len(p.addresses)))
		time.Sleep(1 * time.Second)
	}
}

func (p *Toster) leaderSet(data *types.JSONHeartbeat) {
	if data == nil {
		p.log(nil, "leaderSet: data is nil", ERROR)
		return
	}
	var mu sync.RWMutex
	mu.Lock()
	defer mu.Unlock()
	if p.addr != data.Addresses[0] {
		p.addr = data.Addresses[0]
	}
	p.addresses = data.Addresses
	p.replfactor = data.ReplicFactor
	if p.reconnected {
		p.log(nil, "Reconnected to a database of Warehouse at "+data.Addresses[0], CLI)
		p.log(nil, fmt.Sprintln("Known nodes: ", data.Addresses), CLI)
		p.reconnected = false
	}
}

func (p *Toster) halloCycle() *types.JSONHeartbeat {
	data, err := p.heartbeat()
	if err != nil {
		atomic.StoreInt64(&p.connectedServers, 0)
	}
	for err != nil {
		p.reconnected = true
		p.log(err, "halloCycle: "+p.addr, ERROR)
		for retries := p.postretr; err != nil && retries > 0; retries-- {
			data, err = p.heartbeat()
			time.Sleep(1 * time.Second)
		}
		if err != nil {
			p.rotateIP()
		}
	}
	return data
}

func (p *Toster) rotateIP() {
	p.activeserver++
	if len(p.addresses) < 1 {
		p.log(nil, "No known nodes", ERROR)

		return
	}
	if len(p.addresses) == 1 {
		p.log(nil, "Last node", INFO)
		p.activeserver = 0
	} else if len(p.addresses) <= p.activeserver {
		p.activeserver = 0
	}
	if p.addr != p.addresses[p.activeserver] {
		p.addr = p.addresses[p.activeserver]
	}
}

func (p *Toster) Post(str string) (string, error) {
	for p.connectedServers <= 0 {
		time.Sleep(1 * time.Second)
	}
	req, err := postparser.Postcreator(str)
	if err != nil {
		p.Loger.UserLevel(err.Error())
		return err.Error(), err
	}
	switch req.Method {
	case "GET":
		get, err := p.getCycle(req)

		if get == nil {
			p.log(err, "Post: get nil", ERROR)
			return "", err
		}
		if err != nil {
			return "not found", err
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
	for retries > 0 {
		if p.connectedServers <= 0 {
			time.Sleep(2 * time.Second)
			retries--

			continue
		}
		var list types.PosterResp

		req.Addr = p.proto + p.addr + p.itemapi
		p.getReq(req, &list)

		if list.Err != nil {
			retries--
			time.Sleep(2 * time.Second)

			continue
		} else {

			return &list, nil
		}
	}

	return nil, fmt.Errorf("no servers avalable")
}

func (p *Toster) getReq(req types.PosterReq, res *types.PosterResp) {
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
	} else if resp.StatusCode == http.StatusNotFound {
		res.Status = false
	}
}

func (p *Toster) delCycle(req types.PosterReq) (int, error) {
	retries := p.postretr
	for retries > 0 {
		if p.connectedServers < 0 {
			time.Sleep(3 * time.Second)
			retries--

			continue
		}
		var list types.PosterResp
		req.Addr = p.proto + p.addr + p.itemapi

		p.delReq(req, &list)
		if list.Err != nil {
			retries--

			continue
		} else {
			return list.R.Replicas, nil
		}
	}
	return 0, fmt.Errorf("not found")
}

func (p *Toster) delReq(req types.PosterReq, res *types.PosterResp) {
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
		err = json.NewDecoder(resp.Body).Decode(&res.R)
		if err != nil {
			p.log(err, "delReq, decode json response", ERROR)

			return
		}
		res.Status = true
	}
}

func (p *Toster) postCycle(req types.PosterReq) (int, error) {
	retries := p.postretr
	for retries > 0 {
		if p.connectedServers < 0 {
			time.Sleep(3 * time.Second)
			retries--
			continue
		} else {
			break
		}
	}
	if retries == 0 {
		return 0, fmt.Errorf("not found")
	}

	addrs := make([]string, len(p.addresses))
	copy(addrs, p.addresses)

	var list types.PosterResp

	req.Addr = p.proto + p.addr + p.itemapi
	p.postReq(req, &list)

	return list.R.Replicas, list.Err
}

func (p *Toster) postReq(preq types.PosterReq, res *types.PosterResp) {
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
		p.log(err, "postReq", ERROR)
		log.Println(err)

		return
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		err = json.NewDecoder(resp.Body).Decode(&res.R)
		if err != nil {
			p.log(err, "postReq, decode json response", ERROR)

			return
		}
		res.Status = true
	}
}

func (p *Toster) log(err error, info string, loglevel int) {
	if p.Loger == nil {

		return
	}
	switch loglevel {
	case CLI:
		p.Loger.UserLevel(info)
	case INFO:
		p.Loger.Info(info)
	case ERROR:
		p.Loger.Error(err, info)
	}
}
