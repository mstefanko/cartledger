package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/mstefanko/cartledger/internal/llm"
)

func main() {
	key := os.Getenv("ANTHROPIC_API_KEY")
	if key == "" {
		fmt.Println("ANTHROPIC_API_KEY not set")
		return
	}

	imgPath := "data/receipts/fe4814d0-485a-4767-b871-d350a8802290/1.jpg"
	if len(os.Args) > 1 {
		imgPath = os.Args[1]
	}

	img, err := os.ReadFile(imgPath)
	if err != nil {
		fmt.Printf("read image: %v\n", err)
		return
	}
	fmt.Printf("Image: %d bytes\n", len(img))

	// Use the real ClaudeClient
	_ = option.WithAPIKey(key) // just to keep import
	model := os.Getenv("LLM_MODEL")
	client := llm.NewClaudeClient(key, model)
	fmt.Printf("Provider: %s\n", client.Provider())

	fmt.Println("Calling ExtractReceipt...")
	extraction, err := client.ExtractReceipt([][]byte{img})
	if err != nil {
		fmt.Printf("ERROR: %v\n", err)
		return
	}

	j, _ := json.MarshalIndent(extraction, "", "  ")
	fmt.Printf("Extraction:\n%s\n", string(j))
}
