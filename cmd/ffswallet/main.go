package main

import (
	"log"

	walletapp "github.com/brad1121/FFSWallet/internal/app"
	"github.com/brad1121/FFSWallet/internal/store"
	"github.com/brad1121/FFSWallet/internal/ui"
)

func main() {
	dir, err := store.DefaultDir()
	if err != nil {
		log.Fatal(err)
	}
	svc := walletapp.NewService(dir)
	defer svc.Close()

	ui.Run(svc)
}
