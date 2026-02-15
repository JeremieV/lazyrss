package main
import (
	"fmt"
	"github.com/charmbracelet/glamour"
)
func main() {
	r, _ := glamour.NewTermRenderer(glamour.WithAutoStyle())
	out, _ := r.Render("This is a token: GLAMOURURLTOKEN0")
	fmt.Printf("%q\n", out)
}
