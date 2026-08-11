package main

import (
	"archive/zip"
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

var encryptedMagic = []byte("JOSSBAK2")

type request struct {
	Protocol string        `json:"protocol"`
	ID       string        `json:"id"`
	Method   string        `json:"method"`
	Args     []interface{} `json:"args"`
}

type response struct {
	ID     string      `json:"id"`
	Result interface{} `json:"result,omitempty"`
	Error  interface{} `json:"error,omitempty"`
}

type options struct {
	Mode     string `json:"mode"`
	BackupID string `json:"backup_id"`
	Type     string `json:"type"`
	Provider string `json:"provider"`
	Encrypt  bool   `json:"encrypt"`
	Password string `json:"password"`
	At       string `json:"at"`
	Keep     int    `json:"keep"`
}

func main() {
	var req request
	if err := json.NewDecoder(io.LimitReader(os.Stdin, 16<<20)).Decode(&req); err != nil {
		write(response{Error: rpcError("BAD_REQUEST", err)})
		return
	}
	if req.Protocol != "joss-rpc-v1" {
		write(response{ID: req.ID, Error: rpcError("BAD_PROTOCOL", fmt.Errorf("se requiere joss-rpc-v1"))})
		return
	}
	result, err := dispatch(req.Method, req.Args)
	if err != nil {
		write(response{ID: req.ID, Error: rpcError("BACKUP_ERROR", err)})
		return
	}
	write(response{ID: req.ID, Result: result})
}

func dispatch(method string, args []interface{}) (interface{}, error) {
	switch method {
	case "run", "schedule":
		cfg, err := decodeOptions(args)
		if err != nil {
			return nil, err
		}
		if method == "schedule" {
			return saveSchedule(cfg)
		}
		if cfg.Mode == "restore" {
			return true, restore(cfg)
		}
		return create(cfg)
	case "list":
		provider := stringArg(args, 0, "local")
		if err := requireProvider(provider); err != nil {
			return nil, err
		}
		return list()
	case "delete":
		if len(args) == 0 {
			return nil, fmt.Errorf("delete requiere backup_id")
		}
		if err := requireProvider(stringArg(args, 1, "local")); err != nil {
			return nil, err
		}
		return true, os.Remove(resolveBackup(stringArg(args, 0, "")))
	case "verify":
		if len(args) == 0 {
			return nil, fmt.Errorf("verify requiere backup_id")
		}
		if err := requireProvider(stringArg(args, 1, "local")); err != nil {
			return nil, err
		}
		return true, verify(resolveBackup(stringArg(args, 0, "")), stringArg(args, 2, ""))
	case "test_provider":
		return true, requireProvider(stringArg(args, 0, "local"))
	case "migrate":
		if len(args) < 2 {
			return nil, fmt.Errorf("migrate requiere URL y token")
		}
		return true, migrate(stringArg(args, 0, ""), stringArg(args, 1, ""))
	default:
		return nil, fmt.Errorf("método no soportado: %s", method)
	}
}

func decodeOptions(args []interface{}) (options, error) {
	if len(args) != 1 {
		return options{}, fmt.Errorf("se requiere un objeto de opciones")
	}
	data, _ := json.Marshal(args[0])
	var cfg options
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, err
	}
	if cfg.Mode == "" {
		cfg.Mode = "create"
	}
	if cfg.Type == "" {
		cfg.Type = "full"
	}
	if cfg.Provider == "" {
		cfg.Provider = "local"
	}
	if cfg.Keep <= 0 {
		cfg.Keep = 7
	}
	return cfg, requireProvider(cfg.Provider)
}

func create(cfg options) (string, error) {
	root := projectRoot()
	if err := os.MkdirAll(backupDir(), 0700); err != nil {
		return "", err
	}
	id := "backup_" + time.Now().UTC().Format("2006_01_02_150405")
	plain := filepath.Join(backupDir(), id+".zip.tmp")
	if err := zipProject(root, plain, id, cfg.Type); err != nil {
		_ = os.Remove(plain)
		return "", err
	}
	final := filepath.Join(backupDir(), id+".zip")
	if cfg.Encrypt {
		password := cfg.Password
		if password == "" {
			password = os.Getenv("APP_KEY")
		}
		if password == "" {
			_ = os.Remove(plain)
			return "", fmt.Errorf("encrypt requiere password o APP_KEY")
		}
		final = filepath.Join(backupDir(), id+".jbe")
		if err := encryptFile(plain, final, password); err != nil {
			_ = os.Remove(plain)
			return "", err
		}
		_ = os.Remove(plain)
	} else if err := os.Rename(plain, final); err != nil {
		return "", err
	}
	if !strings.EqualFold(cfg.Provider, "local") {
		uploadRemoteProvider(cfg.Provider, final)
	}
	prune(cfg.Keep)
	return id, nil
}

func restore(cfg options) error {
	archive := resolveBackup(cfg.BackupID)
	if archive == "" {
		return fmt.Errorf("backup no encontrado: %s", cfg.BackupID)
	}
	plain := archive
	if filepath.Ext(archive) == ".jbe" {
		password := cfg.Password
		if password == "" {
			password = os.Getenv("APP_KEY")
		}
		if password == "" {
			return fmt.Errorf("se requiere password o APP_KEY")
		}
		plain = filepath.Join(backupDir(), ".restore-"+fmt.Sprint(time.Now().UnixNano())+".zip")
		defer os.Remove(plain)
		if err := decryptFile(archive, plain, password); err != nil {
			return err
		}
	}
	return unzipSafe(plain, projectRoot())
}

func zipProject(root, destination, id, backupType string) error {
	file, err := os.Create(destination)
	if err != nil {
		return err
	}
	zw := zip.NewWriter(file)
	manifest, _ := json.MarshalIndent(map[string]interface{}{
		"id": id, "format": 2, "type": backupType, "created_at": time.Now().UTC().Format(time.RFC3339),
	}, "", "  ")
	entry, _ := zw.Create("META-INF/joss-backup.json")
	_, _ = entry.Write(manifest)
	err = filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, _ := filepath.Rel(root, path)
		relSlash := filepath.ToSlash(rel)
		if info.IsDir() && (relSlash == ".git" || relSlash == "storage/backups" || strings.HasPrefix(relSlash, "storage/backups/")) {
			return filepath.SkipDir
		}
		if info.IsDir() || !info.Mode().IsRegular() || rel == "." {
			return nil
		}
		if backupType == "database" && !isDatabaseFile(relSlash) {
			return nil
		}
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		header.Name = "project/" + relSlash
		header.Method = zip.Deflate
		writer, err := zw.CreateHeader(header)
		if err != nil {
			return err
		}
		source, err := os.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(writer, source)
		closeErr := source.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
	closeZip := zw.Close()
	closeFile := file.Close()
	if err != nil {
		return err
	}
	if closeZip != nil {
		return closeZip
	}
	return closeFile
}

func unzipSafe(filename, root string) error {
	zr, err := zip.OpenReader(filename)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, file := range zr.File {
		if !strings.HasPrefix(file.Name, "project/") {
			continue
		}
		rel := strings.TrimPrefix(file.Name, "project/")
		clean := filepath.Clean(filepath.FromSlash(rel))
		if clean == "." || clean == ".." || filepath.IsAbs(clean) || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return fmt.Errorf("ruta insegura en backup: %s", file.Name)
		}
		target := filepath.Join(root, clean)
		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		reader, err := file.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, file.Mode())
		if err == nil {
			_, err = io.Copy(out, reader)
			_ = out.Close()
		}
		_ = reader.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func encryptFile(source, destination, password string) error {
	plain, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	key := sha256.Sum256([]byte(password))
	block, _ := aes.NewCipher(key[:])
	gcm, _ := cipher.NewGCM(block)
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	sealed := gcm.Seal(nil, nonce, plain, encryptedMagic)
	data := append(append(append([]byte{}, encryptedMagic...), nonce...), sealed...)
	return os.WriteFile(destination, data, 0600)
}

func decryptFile(source, destination, password string) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	if len(data) < len(encryptedMagic) || !bytes.Equal(data[:len(encryptedMagic)], encryptedMagic) {
		return fmt.Errorf("formato cifrado inválido")
	}
	key := sha256.Sum256([]byte(password))
	block, _ := aes.NewCipher(key[:])
	gcm, _ := cipher.NewGCM(block)
	offset := len(encryptedMagic)
	if len(data) < offset+gcm.NonceSize() {
		return fmt.Errorf("archivo cifrado truncado")
	}
	plain, err := gcm.Open(nil, data[offset:offset+gcm.NonceSize()], data[offset+gcm.NonceSize():], encryptedMagic)
	if err != nil {
		return fmt.Errorf("password incorrecto o backup alterado: %w", err)
	}
	return os.WriteFile(destination, plain, 0600)
}

func verify(filename, password string) error {
	if filename == "" {
		return fmt.Errorf("backup no encontrado")
	}
	plain := filename
	if filepath.Ext(filename) == ".jbe" {
		if password == "" {
			password = os.Getenv("APP_KEY")
		}
		plain = filepath.Join(backupDir(), ".verify-"+fmt.Sprint(time.Now().UnixNano())+".zip")
		defer os.Remove(plain)
		if err := decryptFile(filename, plain, password); err != nil {
			return err
		}
	}
	zr, err := zip.OpenReader(plain)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, file := range zr.File {
		reader, err := file.Open()
		if err != nil {
			return err
		}
		_, err = io.Copy(io.Discard, reader)
		_ = reader.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func list() ([]map[string]interface{}, error) {
	entries, err := os.ReadDir(backupDir())
	if os.IsNotExist(err) {
		return []map[string]interface{}{}, nil
	}
	if err != nil {
		return nil, err
	}
	result := make([]map[string]interface{}, 0)
	for _, entry := range entries {
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if entry.IsDir() || (ext != ".zip" && ext != ".jbe") {
			continue
		}
		info, _ := entry.Info()
		result = append(result, map[string]interface{}{
			"id": strings.TrimSuffix(entry.Name(), ext), "file": entry.Name(), "size": info.Size(),
			"encrypted": ext == ".jbe", "modified_at": info.ModTime().UTC().Format(time.RFC3339),
		})
	}
	sort.Slice(result, func(i, j int) bool {
		return fmt.Sprint(result[i]["modified_at"]) > fmt.Sprint(result[j]["modified_at"])
	})
	return result, nil
}

func saveSchedule(cfg options) (bool, error) {
	if err := os.MkdirAll(filepath.Join(projectRoot(), "storage", "backups"), 0700); err != nil {
		return false, err
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	return true, os.WriteFile(filepath.Join(projectRoot(), "storage", "backups", "schedule.json"), data, 0600)
}

func migrate(target, token string) error {
	items, err := list()
	if err != nil || len(items) == 0 {
		if err != nil {
			return err
		}
		return fmt.Errorf("no hay backups para migrar")
	}
	filename := filepath.Join(backupDir(), fmt.Sprint(items[0]["file"]))
	file, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer file.Close()
	req, err := http.NewRequest(http.MethodPost, target, file)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("X-Joss-Backup-Name", filepath.Base(filename))
	resp, err := (&http.Client{Timeout: 30 * time.Minute}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return fmt.Errorf("destino HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func resolveBackup(id string) string {
	clean := filepath.Base(strings.TrimSpace(id))
	if clean == "." || clean == "" {
		return ""
	}
	for _, name := range []string{clean, clean + ".zip", clean + ".jbe"} {
		candidate := filepath.Join(backupDir(), name)
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
			return candidate
		}
	}
	return ""
}

func prune(keep int) {
	items, _ := list()
	for index := keep; index < len(items); index++ {
		_ = os.Remove(filepath.Join(backupDir(), fmt.Sprint(items[index]["file"])))
	}
}

func projectRoot() string {
	if root := strings.TrimSpace(os.Getenv("JOSS_PROJECT_ROOT")); root != "" {
		return root
	}
	root, _ := os.Getwd()
	return root
}

func backupDir() string {
	if configured := strings.TrimSpace(os.Getenv("BACKUP_PATH")); configured != "" {
		if filepath.IsAbs(configured) {
			return configured
		}
		return filepath.Join(projectRoot(), configured)
	}
	return filepath.Join(projectRoot(), "storage", "backups")
}

func isDatabaseFile(path string) bool {
	lower := strings.ToLower(path)
	return lower == strings.ToLower(os.Getenv("DB_PATH")) || strings.HasSuffix(lower, ".sqlite") || strings.HasSuffix(lower, ".db") || strings.HasSuffix(lower, ".sql")
}

func requireProvider(provider string) error {
	p := strings.ToLower(strings.TrimSpace(provider))
	if p == "local" || p == "gdrive" || p == "google_drive" || p == "s3" || p == "webdav" || p == "remote" || p == "webhook" {
		return nil
	}
	return fmt.Errorf("proveedor de respaldo %q no soportado. Opciones válidas: local, gdrive, s3, webdav, remote", provider)
}

func uploadRemoteProvider(provider, filename string) {
	uploadURL := strings.TrimSpace(os.Getenv("BACKUP_REMOTE_URL"))
	if uploadURL == "" && (provider == "gdrive" || provider == "google_drive") {
		uploadURL = strings.TrimSpace(os.Getenv("GDRIVE_WEBHOOK_URL"))
	}
	if uploadURL == "" && provider == "s3" {
		uploadURL = strings.TrimSpace(os.Getenv("S3_WEBHOOK_URL"))
	}
	if uploadURL == "" && provider == "webdav" {
		uploadURL = strings.TrimSpace(os.Getenv("WEBDAV_WEBHOOK_URL"))
	}
	if uploadURL != "" {
		file, err := os.Open(filename)
		if err != nil {
			return
		}
		defer file.Close()
		req, err := http.NewRequest(http.MethodPost, uploadURL, file)
		if err != nil {
			return
		}
		if token := strings.TrimSpace(os.Getenv("BACKUP_REMOTE_TOKEN")); token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		} else if token := strings.TrimSpace(os.Getenv("GDRIVE_WEBHOOK_TOKEN")); token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		req.Header.Set("Content-Type", "application/octet-stream")
		req.Header.Set("X-Joss-Backup-Name", filepath.Base(filename))
		req.Header.Set("X-Joss-Backup-Provider", provider)
		resp, err := (&http.Client{Timeout: 10 * time.Minute}).Do(req)
		if err == nil {
			_ = resp.Body.Close()
		}
	}
}

func stringArg(args []interface{}, index int, fallback string) string {
	if index >= len(args) || args[index] == nil {
		return fallback
	}
	value := strings.TrimSpace(fmt.Sprint(args[index]))
	if value == "" {
		return fallback
	}
	return value
}

func rpcError(code string, err error) map[string]string {
	return map[string]string{"code": code, "message": err.Error()}
}
func write(value response) { _ = json.NewEncoder(os.Stdout).Encode(value) }
