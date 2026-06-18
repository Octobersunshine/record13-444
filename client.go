package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
)

const (
	serverURL   = "http://localhost:8080"
	chunkSize   = 1 * 1024 * 1024
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: client.exe <file_path>")
		os.Exit(1)
	}

	filePath := os.Args[1]
	if err := uploadFile(filePath); err != nil {
		fmt.Printf("Upload failed: %v\n", err)
		os.Exit(1)
	}
}

func uploadFile(filePath string) error {
	fileData, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read file: %w", err)
	}

	fileID := generateFileID(fileData)
	fileName := filepath.Base(filePath)
	totalChunks := (len(fileData) + chunkSize - 1) / chunkSize

	fileHash := calculateSHA256(fileData)
	fmt.Printf("Uploading file: %s\n", fileName)
	fmt.Printf("File ID: %s\n", fileID)
	fmt.Printf("File size: %d bytes\n", len(fileData))
	fmt.Printf("Total chunks: %d\n", totalChunks)
	fmt.Printf("File hash (SHA256): %s\n\n", fileHash)

	autoMerged := false

	for i := 0; i < totalChunks; i++ {
		start := i * chunkSize
		end := start + chunkSize
		if end > len(fileData) {
			end = len(fileData)
		}
		chunkData := fileData[start:end]
		chunkHash := calculateSHA256(chunkData)

		fmt.Printf("Uploading chunk %d/%d (size: %d bytes, hash: %s)...\n", i+1, totalChunks, len(chunkData), chunkHash[:16]+"...")

		merged, err := uploadChunk(fileID, fileName, fileHash, i, totalChunks, chunkHash, chunkData)
		if err != nil {
			return fmt.Errorf("chunk %d upload failed: %w", i, err)
		}
		if merged {
			autoMerged = true
		}

		if !autoMerged {
			if err := checkStatus(fileID); err != nil {
				fmt.Printf("Status check warning: %v\n", err)
			}
		}
	}

	if !autoMerged {
		fmt.Println("\nAll chunks uploaded. Merging file...")
		if err := mergeFile(fileID); err != nil {
			return fmt.Errorf("merge failed: %w", err)
		}
	} else {
		fmt.Println("\nFile was auto-merged during upload. No manual merge needed.")
	}

	return nil
}

func uploadChunk(fileID, fileName, fileHash string, chunkIndex, totalChunks int, chunkHash string, chunkData []byte) (bool, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	writer.WriteField("file_id", fileID)
	writer.WriteField("file_name", fileName)
	writer.WriteField("file_hash", fileHash)
	writer.WriteField("chunk_index", fmt.Sprintf("%d", chunkIndex))
	writer.WriteField("total_chunks", fmt.Sprintf("%d", totalChunks))
	writer.WriteField("chunk_hash", chunkHash)

	part, err := writer.CreateFormFile("file", fmt.Sprintf("chunk_%d", chunkIndex))
	if err != nil {
		return false, err
	}
	if _, err := part.Write(chunkData); err != nil {
		return false, err
	}
	writer.Close()

	req, err := http.NewRequest("POST", serverURL+"/upload", body)
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	var result map[string]interface{}
	json.Unmarshal(respBody, &result)

	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("server returned %d: %s", resp.StatusCode, string(respBody))
	}

	if success, ok := result["success"].(bool); ok && !success {
		msg, _ := result["message"].(string)
		return false, fmt.Errorf("upload failed: %s", msg)
	}

	verified, _ := result["verified"].(bool)
	merged, _ := result["merged"].(bool)
	fmt.Printf("  -> Chunk %d uploaded successfully (verified: %v)\n", chunkIndex, verified)

	if merged {
		fmt.Println("\n=== Auto-Merge Triggered ===")
		if path, ok := result["file_path"].(string); ok {
			fmt.Printf("Merged file path: %s\n", path)
		}
		if hash, ok := result["file_hash"].(string); ok {
			fmt.Printf("Merged file hash: %s\n", hash)
		}
		if hashMatch, ok := result["hash_match"].(bool); ok {
			fmt.Printf("File hash verified: %v\n", hashMatch)
		}
	}
	return merged, nil
}

func checkStatus(fileID string) error {
	resp, err := http.Get(fmt.Sprintf("%s/status?file_id=%s", serverURL, fileID))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	var result map[string]interface{}
	json.Unmarshal(respBody, &result)

	if progress, ok := result["progress"].(string); ok {
		fmt.Printf("  -> Progress: %s\n", progress)
	}
	return nil
}

func mergeFile(fileID string) error {
	reqBody, _ := json.Marshal(map[string]string{"file_id": fileID})
	resp, err := http.Post(serverURL+"/merge", "application/json", bytes.NewBuffer(reqBody))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	var result map[string]interface{}
	json.Unmarshal(respBody, &result)

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server returned %d: %s", resp.StatusCode, string(respBody))
	}

	if success, ok := result["success"].(bool); ok && !success {
		msg, _ := result["message"].(string)
		return fmt.Errorf("merge failed: %s", msg)
	}

	fmt.Println("\n=== Merge Complete ===")
	if path, ok := result["file_path"].(string); ok {
		fmt.Printf("Merged file path: %s\n", path)
	}
	if hash, ok := result["file_hash"].(string); ok {
		fmt.Printf("Merged file hash: %s\n", hash)
	}
	if verified, ok := result["verified"].(bool); ok {
		fmt.Printf("File hash verified: %v\n", verified)
	}
	if msg, ok := result["message"].(string); ok {
		fmt.Printf("Message: %s\n", msg)
	}

	return nil
}

func calculateSHA256(data []byte) string {
	h := sha256.New()
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}

func generateFileID(data []byte) string {
	h := sha256.New()
	h.Write([]byte(fmt.Sprintf("%d_%d", len(data), os.Getpid())))
	h.Write(data[:min(1024, len(data))])
	return hex.EncodeToString(h.Sum(nil))[:16]
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
