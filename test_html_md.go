package main
import (
	"fmt"
	"github.com/JohannesKaufmann/html-to-markdown/v2"
)
func main() {
	html := "<p>Text before <img src=\"https://example.com/img.png\" alt=\"Alt\"> and after.</p>"
	md, _ := htmltomarkdown.ConvertString(html)
	fmt.Printf("%q\n", md)
}
