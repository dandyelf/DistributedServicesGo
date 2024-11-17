package bloger

import (
	"fmt"
	"log"
)

type Bloger struct {
	Loglevel int
}

func (b *Bloger) UserLevel(info string) {
	if b.Loglevel >= 0 {
		fmt.Println(info)
		fmt.Println(">")
	}
}

func (b *Bloger) Error(err error, who string) {
	if b.Loglevel > 0 {
		log.Println(who, err)
		log.Println(">")
	}
}

func (b *Bloger) Info(info string) {
	if b.Loglevel > 1 {
		fmt.Println(info)
		fmt.Println(">")
	}
}
