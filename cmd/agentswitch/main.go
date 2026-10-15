package main

import (
	"log"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func main() {
	mygo.App.WhenReady(func() {
		mygo.NewWindow(mygo.WindowOptions{
			Title:  "AgentSwitch",
			Width:  1080,
			Height: 720,
			Content: ui.View(func(c *ui.Context) {
				ui.Column(c).Gap(12).Children(func() {
					ui.Text(c, "AgentSwitch")
				})
			}),
		})
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}