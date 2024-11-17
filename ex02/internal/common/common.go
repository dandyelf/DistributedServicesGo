package common

import (
	"crypto/sha1"

	"github.com/google/uuid"
)

func HashFunction(addrs *[]string, num int, id string) *[]string {
	var s []string
	for i := 0; i < num; i++ {
		r := HashCalc(len(*addrs), id)
		a := (*addrs)[r]
		s = append(s, a)
		*addrs = append((*addrs)[:r], (*addrs)[r+1:]...)
	}
	return &s
}

func HashCalc(n int, s string) int {
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
