package main

import (
	"fmt"
	"log"
	"net/http"
)

func main() {
	fmt.Println("Serving http")
	log.Fatal(http.ListenAndServe(":3000", http.FileServer(http.Dir("./"))))
}
