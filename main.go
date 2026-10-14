package main

import "os"

type Page struct {
	Title string
	Body  []byte
}

func (p *Page) save() error {
	filename := p.Title + ".txt"
	return os.WriteFile(filename, p.Body, 0600)
}

func loadPage(title string) (*Page, error) {
	filename := title + ".txt"
	body, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	return &Page{Title: title, Body: body}, nil
}



func main() {
	p1 := &Page{Title: "test", Body: []byte("This is a sample page.")}
	p1.save()

	p2, _ := loadPage(p1.Title)
	println(string(p2.Body))
}