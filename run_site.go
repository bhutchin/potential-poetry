package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
)

func main() {
	http.HandleFunc("/", logRequest(genericFileHandler("root.html")))
	http.HandleFunc("/new_page", logRequest(genericFileHandler("new_page.html")))
	http.HandleFunc("/static/", staticFileHandler)
	http.HandleFunc("/submit_recipe", logRequest(genericFileHandler("submit_recipe.html"))) // This now correctly points to the recipe form
	http.HandleFunc("/submit_ingredients", logRequest(submitIngredientsHandler))
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

func submitIngredientsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Failed to parse form", http.StatusBadRequest)
		return
	}

	// The form data is available in r.PostForm.
	// You can now process it, for example, by saving it to the database.
	log.Println("Received ingredients submission:")
	for key, values := range r.PostForm {
		log.Printf("  %s: %v\n", key, values)
	}

	// Redirect the user back to the recipe page after submission.
	http.Redirect(w, r, "/submit_recipe", http.StatusSeeOther)
}
