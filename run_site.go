package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
)

var templateFile string = "web/root.html"
var newPageTemplateFile string = "web/new_page.html"

func main() {
	http.HandleFunc("/", logRequest(homeHandler))
	http.HandleFunc("/new_page", logRequest(newPageHandler))
	http.HandleFunc("/static/", staticFileHandler)
	http.HandleFunc("/custom_page", genericFileHandler("custom_page.html"))
	fmt.Println("Starting web server..")
	http.ListenAndServe(":8080", nil)
}

func logRequest(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s %s\n", r.RemoteAddr, r.Method, r.URL)
		handler(w, r)
	}
}

func readFile(path_to_file string) (string, error) {
	data, err := os.ReadFile(path_to_file)
	if err != nil {
		return "", err
	}

	return string(data), nil
}

func homeHandler(website http.ResponseWriter, r *http.Request) {
	fileContent, err := readFile(templateFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading file: %v\n", err)
		http.Error(website, "Failed to load content", http.StatusInternalServerError)
		return
	}
	website.Header().Set("Content-Type", "text/html")
	fmt.Fprint(website, fileContent)
}

func genericFileHandler(filePath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		fileContent, err := readFile("web/" + filePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading file: %v\n", err)
			http.Error(w, "Failed to load content", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, fileContent)
	}
}

func staticFileHandler(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path[len("/static/"):] // Strip "/static/" prefix
	if path == "" {
		http.NotFound(w, r)
		return
	}

	fullPath := filepath.Join("web", "static", path)
	http.ServeFile(w, r, fullPath)

	// content, err := os.ReadFile(fullPath)
	// if err != nil {
	// 	http.Error(w, "File not found", http.StatusNotFound)
	// 	return
	// }
	// w.Header().Set("Content-Type", "text/css")
	// w.Write(content)
}

func newPageHandler(website http.ResponseWriter, r *http.Request) {
	fileContent, err := readFile(newPageTemplateFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading file: %v\n", err)
		http.Error(website, "Failed to load content", http.StatusInternalServerError)
		return
	}

	website.Header().Set("Content-Type", "text/html")
	fmt.Fprint(website, fileContent)
}
