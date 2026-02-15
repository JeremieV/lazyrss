package main
import (
	"fmt"
	"github.com/charmbracelet/glamour"
)
func main() {
	r, _ := glamour.NewTermRenderer(glamour.WithAutoStyle())
	out, _ := r.Render("Check this \x1b]8;;https://google.com\x1b\\Click Me\x1b]8;;\x1b\\")
	fmt.Printf("%q\n", out)
}
