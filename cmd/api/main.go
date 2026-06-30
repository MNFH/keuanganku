package main

import (
	"fmt"
	"log"
	"net/http"
)

func main() {
	fmt.Println("Keuanganku API starting on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
