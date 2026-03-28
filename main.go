package main

import "github.com/lucasroesler/podstash/pkg/podstash"

func main() {
	cfg := podstash.LoadConfig()
	podstash.Run(cfg)
}
