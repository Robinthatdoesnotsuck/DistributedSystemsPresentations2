package main

import (
	"fmt"
	"time"
)

func main() {
	ch := make(chan string, 3)
	for _, s := range []string{"a", "b", "c"} {
		go correctClosure(ch)
		go func(ch chan<- string) {
			// send
			ch <- "Oli de go rutina"
			fmt.Printf("Do something %s \n", s)
		}(ch)
	}
	time.Sleep(100000)
}

func correctClosure(ch <-chan string) {
	msg := <-ch
	fmt.Printf("Goroutine message %s \n", msg)
}
