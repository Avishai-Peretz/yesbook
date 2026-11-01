package main

import (
	"fmt"

	"github.com/avi-p/go_first_project/greetings"
)

func main() {
	fmt.Println("Hello, Go!")

	names := []string{"Avi", "Gopher", ""}
	for _, name := range names {
		msg, err := greetings.Hello(name)
		if err != nil {
			fmt.Println("error:", err)
			continue
		}
		fmt.Println(msg)
	}
}
