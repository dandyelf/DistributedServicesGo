package postparser

import (
	"21school/warehouse/types"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

func Postcreator(s string) (types.PosterReq, error) {
	var pr types.PosterReq
	parts := strings.Split(s, " ")
	if len(parts) < 2 {
		return pr, fmt.Errorf("not a proper input")
	}

	reqtype, err := reqtypeCheck(parts[0])
	if err != nil {
		return pr, err
	}
	pr.Method = reqtype
	err = uuid.Validate(parts[1])
	if err != nil {
		return pr, err
	}
	pr.Formquery = map[string]string{"uuid": parts[1]}
	if reqtype == "POST" {
		jsonstr := strings.Join(parts[2:], " ")
		j, err := deserialize(jsonstr)
		if err != nil {
			return pr, err
		}
		pr.Req = j
	}
	return pr, nil
}

func deserialize(s string) (types.Req, error) {
	var j types.Req
	s = strings.TrimRight(s, "' ")
	s = strings.TrimLeft(s, "'")
	err := json.Unmarshal([]byte(s), &j)
	if err != nil {

		return j, err
	}

	return j, nil
}

func reqtypeCheck(s string) (string, error) {
	switch s {
	case "SET":
		return "POST", nil
	case "GET":
		return "GET", nil
	case "DELETE":
		return "DELETE", nil
	}
	return "", fmt.Errorf("key is not a proper method")
}
