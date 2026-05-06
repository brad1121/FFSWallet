package main

import (
	"log"

	walletapp "github.com/brad1121/FFSWallet/internal/app"
	"github.com/brad1121/FFSWallet/internal/store"
	"github.com/brad1121/FFSWallet/internal/ui"
)

func main() {
	path, err := store.DefaultPath()
	if err != nil {
		log.Fatal(err)
	}
	svc := walletapp.NewService(path)
	defer svc.Close()

	ui.Run(svc)
}
