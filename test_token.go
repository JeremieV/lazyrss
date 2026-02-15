package main
import (
	"fmt"
	"strings"
	"github.com/charmbracelet/glamour"
)
func main() {
	r, _ := glamour.NewTermRenderer(glamour.WithAutoStyle())
	md := "Check this __LINK_1__ and this __LINK_2__."
	out, _ := r.Render(md)
	fmt.Printf("%q\n", out)
	
	final := strings.ReplaceAll(out, "__LINK_1__", "\x1b]8;;https://google.com\x1b\Google\x1b]8;;\x1b\")
	fmt.Println(final)
}
