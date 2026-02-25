package main

import "github.com/fatih/color"

var Dollar string

var (
	GreenBold func(string, ...any) string
)

func init() {
	cl := color.New(color.FgGreen, color.Bold)
	GreenBold = cl.Sprintf
	Dollar = GreenBold("$")
}
