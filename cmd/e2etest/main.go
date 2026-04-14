package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"
	"time"

	"github.com/mstefanko/cartledger/internal/auth"
)

func main() {
	base := "http://localhost:8079/api/v1"
	client := &http.Client{Timeout: 120 * time.Second}

	// Step 1: Generate token directly
	fmt.Println("=== STEP 1: Generate token ===")
	token, err := auth.CreateAuthToken(
		"change-me-in-production",
		"e30e0348-40c5-4a0f-aeea-0d5eb454e333",
		"eda83733-9b9e-42b8-8cab-85329cd5207a",
	)
	if err != nil {
		fmt.Printf("token err: %v\n", err)
		return
	}
	fmt.Printf("Token: %s...\n", token[:30])

	// Step 2: Upload receipt
	fmt.Println("\n=== STEP 2: Upload receipt ===")
	imgPath := "data/receipts/c9ddbb53-b666-42ad-b2ad-b154b448c7d5/1.jpg"
	imgFile, err := os.Open(imgPath)
	if err != nil {
		fmt.Printf("open image: %v\n", err)
		return
	}
	defer imgFile.Close()

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="images"; filename="%s"`, filepath.Base(imgPath)))
	h.Set("Content-Type", "image/jpeg")
	part, _ := writer.CreatePart(h)
	io.Copy(part, imgFile)
	writer.Close()

	req, _ := http.NewRequest("POST", base+"/receipts/scan", &buf)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("upload err: %v\n", err)
		return
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	fmt.Printf("Status: %d, Body: %s\n", resp.StatusCode, string(body))

	var scanResp map[string]interface{}
	json.Unmarshal(body, &scanResp)
	receiptID, _ := scanResp["id"].(string)
	if receiptID == "" {
		fmt.Println("No receipt ID!")
		return
	}

	// Step 3: Poll for completion
	fmt.Println("\n=== STEP 3: Poll for completion ===")
	for i := 0; i < 40; i++ {
		time.Sleep(3 * time.Second)
		req, _ = http.NewRequest("GET", base+"/receipts/"+receiptID, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err = client.Do(req)
		if err != nil {
			fmt.Printf("poll err: %v\n", err)
			continue
		}
		body, _ = io.ReadAll(resp.Body)
		resp.Body.Close()

		var receipt map[string]interface{}
		json.Unmarshal(body, &receipt)
		status, _ := receipt["status"].(string)
		storeName, _ := receipt["store_name"].(string)
		lineItems, _ := receipt["line_items"].([]interface{})
		liCount := len(lineItems)

		fmt.Printf("[%ds] status=%s store=%s items=%d\n", (i+1)*3, status, storeName, liCount)

		if status != "pending" && status != "processing" {
			fmt.Println("\n=== STEP 4: Line item details ===")
			for j, li := range lineItems {
				if j >= 5 {
					break
				}
				item, _ := li.(map[string]interface{})
				fmt.Printf("  [%d] raw_name=%s\n", j, item["raw_name"])
				fmt.Printf("      suggested_name=%v\n", item["suggested_name"])
				fmt.Printf("      suggested_category=%v\n", item["suggested_category"])
				fmt.Printf("      suggested_product_id=%v\n", item["suggested_product_id"])
				fmt.Printf("      suggestion_type=%v\n", item["suggestion_type"])
				fmt.Printf("      matched=%v product_id=%v\n", item["matched"], item["product_id"])
			}

			if status == "error" {
				fmt.Println("\n=== RECEIPT ERRORED ===")
				rawJSON, _ := json.MarshalIndent(receipt, "", "  ")
				fmt.Println(string(rawJSON))
			}

			fmt.Printf("\n=== RESULT: %s with %d items ===\n", status, liCount)
			return
		}
	}
	fmt.Println("TIMEOUT - receipt never completed after 2 minutes")
}
