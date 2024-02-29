package main

import (
	"context"
	"go-tg.com/internal/app"
	"os"
	"os/signal"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if err := app.Run(ctx); err != nil {
		panic(err)
	}
}
