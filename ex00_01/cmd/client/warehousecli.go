package main

import (
	"21school/warehouse/internal/toster"
	"bufio"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
)

// warehouse-cli -H 127.0.0.1 -P 8765

type Poster interface {
	Connect(addrress string) error
	UpdateIPs()
	Post(scandata string) (string, error)
}

type flags struct {
	H string `short:"Host"`
	P int    `short:"Port"`
}

var fl flags

func init() {
	flag.StringVar(&fl.H, "H", "127.0.0.1", "H flag, host default localhost")
	flag.IntVar(&fl.P, "P", 8888, "P flag, port default 8888")
	flag.Parse()
}

func main() {
	var poster toster.Toster
	unstoppablePostGen(fl.H+":"+fmt.Sprint(fl.P), &poster)
}

func unstoppablePostGen(addrress string, p Poster) {
	err := p.Connect(addrress)
	if err != nil {
		log.Fatal("connection err: ", err)
	}
	go p.UpdateIPs()
	fmt.Println("wait for user input")
	for err == nil {
		s := scaner()
		str, err := p.Post(s)
		if err != nil {
			log.Println(err)
			continue
		}
		fmt.Println(str)
	}
}

func scaner() string {
	fmt.Println(">")
	reader := bufio.NewReader(os.Stdin)
	message, _ := reader.ReadString('\n')
	message = strings.Replace(message, "\n", "", -1)
	return message
}
