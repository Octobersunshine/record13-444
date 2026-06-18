# Go HTTP 文件分片上传与校验服务器

基于 Go 标准库实现的文件分片上传服务器，支持接收分片序号与分片哈希，并校验分片完整性。

## 功能特性

- **分片上传**：支持大文件分片上传，每片最大 100MB
- **哈希校验**：使用 SHA-256 校验每个分片的完整性，防止传输过程中数据损坏
- **断点续传**：支持查询上传进度，已上传分片无需重复上传
- **文件合并**：所有分片上传完成后，按顺序合并为完整文件
- **整体校验**：合并后对完整文件进行 SHA-256 校验，确保文件一致性
- **并发安全**：内置互斥锁，支持并发上传

## 项目结构

```
.
├── main.go      # 服务端主程序
├── client.go    # 测试客户端（演示如何分片上传）
├── go.mod       # Go 模块文件
└── uploads/     # 上传文件目录（自动创建）
    ├── chunks/  # 分片临时存储
    └── merged/  # 合并后完整文件
```

## API 接口

### 1. 上传分片 `POST /upload`

**Content-Type**: `multipart/form-data`

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| file_id | string | 是 | 文件唯一标识（同一文件的所有分片使用相同 ID） |
| file_name | string | 是 | 原始文件名 |
| file_hash | string | 否 | 完整文件的 SHA-256（用于合并后整体校验） |
| chunk_index | int | 是 | 分片序号（从 0 开始） |
| total_chunks | int | 是 | 总分片数 |
| chunk_hash | string | 是 | 当前分片数据的 SHA-256 |
| file | file | 是 | 分片二进制数据 |

**响应示例**：

成功：
```json
{
  "success": true,
  "message": "Chunk uploaded and verified successfully",
  "chunk_index": 0,
  "verified": true
}
```

失败（哈希不匹配）：
```json
{
  "success": false,
  "message": "Chunk hash mismatch. Expected: xxx, Calculated: yyy",
  "chunk_index": 0,
  "verified": false
}
```

### 2. 查询上传状态 `GET /status`

**Query 参数**：
- `file_id`: 文件唯一标识

**响应示例**：
```json
{
  "success": true,
  "file_id": "abc123",
  "file_name": "test.zip",
  "total_chunks": 5,
  "received": 3,
  "progress": "60%",
  "completed": false
}
```

### 3. 合并文件 `POST /merge`

**Content-Type**: `application/json`

**请求体**：
```json
{
  "file_id": "abc123"
}
```

**响应示例**：
```json
{
  "success": true,
  "message": "File merged successfully",
  "file_path": "./uploads/merged/test.zip",
  "file_hash": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
  "verified": true
}
```

## 快速开始

### 启动服务端

```bash
go run main.go
```

服务默认监听 `:8080` 端口。

### 使用测试客户端上传文件

```bash
go run client.go <文件路径>
```

示例：
```bash
go run client.go ./test.zip
```

## 核心校验逻辑

### 分片校验流程

1. 客户端计算分片数据的 SHA-256 哈希值
2. 客户端上传分片数据 + 哈希值到服务端
3. 服务端接收分片数据后，重新计算 SHA-256
4. 服务端比对计算值与客户端上传值
   - 一致：校验通过，存储分片，返回 `verified: true`
   - 不一致：校验失败，拒绝存储，返回错误信息

### 文件合并校验

1. 按 `chunk_index` 顺序读取所有分片
2. 拼接写入完整文件
3. 计算完整文件的 SHA-256
4. 与客户端上传的 `file_hash` 比对（如果提供了）
5. 清理临时分片文件
