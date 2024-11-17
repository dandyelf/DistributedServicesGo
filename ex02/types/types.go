package types

type PosterReq struct {
	Addr      string
	Req       Req
	Method    string
	Formquery map[string]string
}

type PosterResp struct {
	Err    error
	Data   Req
	Status bool
	R      Resp
}

// Types for REST API

type Req struct {
	Name string `json:"name"`
}

type Resp struct {
	Replicas int `json:"replicas"`
}

type JSONHeartbeat struct {
	Addresses    []string `json:"addresses"`
	ReplicFactor int      `json:"replicFactor"`
}

type JSONHelloReq struct {
	Address      string `json:"address"`
	ReplicFactor int    `json:"replicFactor"`
}
