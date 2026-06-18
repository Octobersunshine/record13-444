package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
)

const (
	uploadDir    = "./uploads"
	chunksDir    = "./uploads/chunks"
	mergedDir    = "./uploads/merged"
	maxChunkSize = 100 * 1024 * 1024
)

type ChunkInfo struct {
	FileID    string `json:"file_id"`
	ChunkIndex int    `json:"chunk_index"`
	TotalChunks int  `json:"total_chunks"`
	FileName  string `json:"file_name"`
	FileHash  string `json:"file_hash"`
	ChunkHash string `json:"chunk_hash"`
}

type FileStatus struct {
	FileID      string
	FileName    string
	TotalChunks int
	Received    map[int]bool
	ChunkHashes map[int]string
	FileHash    string
	mu          sync.Mutex
}

type UploadResponse struct {
	Success   bool   `json:"success"`
	Message   string `json:"message"`
	ChunkIndex int   `json:"chunk_index,omitempty"`
	Verified  bool   `json:"verified,omitempty"`
}

type MergeResponse struct {
	Success  bool   `json:"success"`
	Message  string `json:"message"`
	FilePath string `json:"file_path,omitempty"`
	FileHash string `json:"file_hash,omitempty"`
	Verified bool   `json:"verified,omitempty"`
}

type StatusResponse struct {
	Success     bool   `json:"success"`
	FileID      string `json:"file_id"`
	FileName    string `json:"file_name"`
	TotalChunks int    `json:"total_chunks"`
	Received    int    `json:"received"`
	Progress    string `json:"progress"`
	Completed   bool   `json:"completed"`
}

var (
	fileStatuses = make(map[string]*FileStatus)
	statusMu     sync.RWMutex
)

func init() {
	os.MkdirAll(chunksDir, 0755)
	os.MkdirAll(mergedDir, 0755)
}

func main() {
	http.HandleFunc("/upload", handleUpload)
	http.HandleFunc("/merge", handleMerge)
	http.HandleFunc("/status", handleStatus)

	fmt.Println("Server starting on :8080")
	fmt.Println("Endpoints:")
	fmt.Println("  POST /upload  - Upload a file chunk")
	fmt.Println("  POST /merge   - Merge all chunks for a file")
	fmt.Println("  GET  /status  - Check upload status for a file")
	http.ListenAndServe(":8080", nil)
}

func handleUpload(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, UploadResponse{Success: false, Message: "Method not allowed"})
		return
	}

	chunkIndexStr := r.FormValue("chunk_index")
	totalChunksStr := r.FormValue("total_chunks")
	fileID := r.FormValue("file_id")
	fileName := r.FormValue("file_name")
	fileHash := r.FormValue("file_hash")
	chunkHash := r.FormValue("chunk_hash")

	if fileID == "" || fileName == "" || chunkHash == "" {
		writeJSON(w, http.StatusBadRequest, UploadResponse{Success: false, Message: "Missing required fields: file_id, file_name, chunk_hash"})
		return
	}

	chunkIndex, err := strconv.Atoi(chunkIndexStr)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, UploadResponse{Success: false, Message: "Invalid chunk_index"})
		return
	}

	totalChunks, err := strconv.Atoi(totalChunksStr)
	if err != nil || totalChunks <= 0 {
		writeJSON(w, http.StatusBadRequest, UploadResponse{Success: false, Message: "Invalid total_chunks"})
		return
	}

	if chunkIndex < 0 || chunkIndex >= totalChunks {
		writeJSON(w, http.StatusBadRequest, UploadResponse{Success: false, Message: "chunk_index out of range"})
		return
	}

	file, _, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, UploadResponse{Success: false, Message: "Failed to read file chunk: " + err.Error()})
		return
	}
	defer file.Close()

	chunkData, err := io.ReadAll(file)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, UploadResponse{Success: false, Message: "Failed to read chunk data"})
		return
	}

	if len(chunkData) == 0 {
		writeJSON(w, http.StatusBadRequest, UploadResponse{Success: false, Message: "Empty chunk data"})
		return
	}

	if len(chunkData) > maxChunkSize {
		writeJSON(w, http.StatusBadRequest, UploadResponse{Success: false, Message: "Chunk size exceeds maximum limit"})
		return
	}

	calculatedHash := calculateSHA256(chunkData)
	if calculatedHash != chunkHash {
		writeJSON(w, http.StatusBadRequest, UploadResponse{
			Success:    false,
			Message:    fmt.Sprintf("Chunk hash mismatch. Expected: %s, Calculated: %s", chunkHash, calculatedHash),
			ChunkIndex: chunkIndex,
			Verified:   false,
		})
		return
	}

	fileChunksDir := getChunksDirForFile(fileID)
	if err := os.MkdirAll(fileChunksDir, 0755); err != nil {
		writeJSON(w, http.StatusInternalServerError, UploadResponse{Success: false, Message: "Failed to create chunks directory: " + err.Error()})
		return
	}

	chunkPath := filepath.Join(fileChunksDir, fmt.Sprintf("%d", chunkIndex))
	if err := os.WriteFile(chunkPath, chunkData, 0644); err != nil {
		writeJSON(w, http.StatusInternalServerError, UploadResponse{Success: false, Message: "Failed to save chunk: " + err.Error()})
		return
	}

	statusMu.Lock()
	fs, exists := fileStatuses[fileID]
	if !exists {
		fs = &FileStatus{
			FileID:      fileID,
			FileName:    fileName,
			TotalChunks: totalChunks,
			Received:    make(map[int]bool),
			ChunkHashes: make(map[int]string),
			FileHash:    fileHash,
		}
		fileStatuses[fileID] = fs
	}
	statusMu.Unlock()

	fs.mu.Lock()
	fs.Received[chunkIndex] = true
	fs.ChunkHashes[chunkIndex] = calculatedHash
	if fs.TotalChunks == 0 {
		fs.TotalChunks = totalChunks
	}
	if fs.FileName == "" {
		fs.FileName = fileName
	}
	if fs.FileHash == "" {
		fs.FileHash = fileHash
	}
	fs.mu.Unlock()

	writeJSON(w, http.StatusOK, UploadResponse{
		Success:    true,
		Message:    "Chunk uploaded and verified successfully",
		ChunkIndex: chunkIndex,
		Verified:   true,
	})
}

func handleMerge(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, MergeResponse{Success: false, Message: "Method not allowed"})
		return
	}

	var req struct {
		FileID string `json:"file_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, MergeResponse{Success: false, Message: "Invalid request body"})
		return
	}

	if req.FileID == "" {
		writeJSON(w, http.StatusBadRequest, MergeResponse{Success: false, Message: "file_id is required"})
		return
	}

	statusMu.RLock()
	fs, exists := fileStatuses[req.FileID]
	statusMu.RUnlock()

	if !exists {
		writeJSON(w, http.StatusNotFound, MergeResponse{Success: false, Message: "File ID not found"})
		return
	}

	fs.mu.Lock()
	defer fs.mu.Unlock()

	if len(fs.Received) != fs.TotalChunks {
		writeJSON(w, http.StatusBadRequest, MergeResponse{
			Success: false,
			Message: fmt.Sprintf("Not all chunks received. Got %d of %d", len(fs.Received), fs.TotalChunks),
		})
		return
	}

	chunkIndices := make([]int, 0, fs.TotalChunks)
	for i := 0; i < fs.TotalChunks; i++ {
		if !fs.Received[i] {
			writeJSON(w, http.StatusBadRequest, MergeResponse{
				Success: false,
				Message: fmt.Sprintf("Missing chunk %d", i),
			})
			return
		}
		chunkIndices = append(chunkIndices, i)
	}
	sort.Ints(chunkIndices)

	fileChunksDir := getChunksDirForFile(req.FileID)

	mergedPath := filepath.Join(mergedDir, fs.FileName)
	outFile, err := os.Create(mergedPath)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, MergeResponse{Success: false, Message: "Failed to create merged file: " + err.Error()})
		return
	}
	defer outFile.Close()

	h := sha256.New()
	for _, idx := range chunkIndices {
		chunkPath := filepath.Join(fileChunksDir, fmt.Sprintf("%d", idx))
		chunkData, err := os.ReadFile(chunkPath)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, MergeResponse{Success: false, Message: "Failed to read chunk: " + err.Error()})
			return
		}

		if _, err := outFile.Write(chunkData); err != nil {
			writeJSON(w, http.StatusInternalServerError, MergeResponse{Success: false, Message: "Failed to write merged file: " + err.Error()})
			return
		}

		h.Write(chunkData)
	}

	mergedHash := hex.EncodeToString(h.Sum(nil))
	hashVerified := true
	if fs.FileHash != "" && mergedHash != fs.FileHash {
		hashVerified = false
	}

	os.RemoveAll(fileChunksDir)

	writeJSON(w, http.StatusOK, MergeResponse{
		Success:  true,
		Message:  "File merged successfully",
		FilePath: mergedPath,
		FileHash: mergedHash,
		Verified: hashVerified,
	})
}

func handleStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, StatusResponse{Success: false})
		return
	}

	fileID := r.URL.Query().Get("file_id")
	if fileID == "" {
		writeJSON(w, http.StatusBadRequest, StatusResponse{Success: false})
		return
	}

	statusMu.RLock()
	fs, exists := fileStatuses[fileID]
	statusMu.RUnlock()

	if !exists {
		writeJSON(w, http.StatusNotFound, StatusResponse{Success: false})
		return
	}

	fs.mu.Lock()
	received := len(fs.Received)
	total := fs.TotalChunks
	fileName := fs.FileName
	completed := received == total && total > 0
	fs.mu.Unlock()

	progress := "0%"
	if total > 0 {
		progress = fmt.Sprintf("%d%%", received*100/total)
	}

	writeJSON(w, http.StatusOK, StatusResponse{
		Success:     true,
		FileID:      fileID,
		FileName:    fileName,
		TotalChunks: total,
		Received:    received,
		Progress:    progress,
		Completed:   completed,
	})
}

func getChunksDirForFile(fileID string) string {
	return filepath.Join(chunksDir, fileID)
}

func calculateSHA256(data []byte) string {
	h := sha256.New()
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
