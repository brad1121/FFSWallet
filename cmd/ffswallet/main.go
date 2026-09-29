package main

import (
	"log"

	walletapp "github.com/brad1121/FFSWallet/internal/app"
	"github.com/brad1121/FFSWallet/internal/ui"
)

func main() {
	a := ui.NewApp()
	dir, err := ui.DataDir(a)
	if err != nil {
		log.Fatal(err)
	}
	svc := walletapp.NewService(dir)
	defer svc.Close()

	ui.Run(a, svc)
}
