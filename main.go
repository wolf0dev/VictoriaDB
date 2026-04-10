package main

import (
	"bufio"
	"database/sql"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"victoriadb/pkg/api"
	"victoriadb/pkg/auth"
	"victoriadb/pkg/core"
	"victoriadb/pkg/db"
)

//go:embed ui/dist
var embeddedUI embed.FS

// VictoriaKernel is the central runtime of VictoriaDB.
type VictoriaKernel struct {
	Manager *db.Manager
	Hub     *api.SSEHub
	Secret  []byte
	Port    int
	DataDir string
}

func main() {
	port := flag.Int("port", 8090, "Port for VictoriaDB to listen on")
	dataDir := flag.String("data", "./victoria_data", "Directory to store VictoriaDB data files")
	setup := flag.Bool("setup", false, "Create the first admin user interactively")
	resetAdmin := flag.Bool("reset-admin", false, "Delete all admins and create a fresh one (use with --admin-user and --admin-pass)")
	adminUser := flag.String("admin-user", "admin", "Username for --reset-admin")
	adminPass := flag.String("admin-pass", "", "Password for --reset-admin")
	flag.Parse()

	if err := os.MkdirAll(*dataDir, 0755); err != nil {
		log.Fatalf("FATAL: could not create data directory: %v", err)
	}

	dbPath := filepath.Join(*dataDir, "victoria.db")
	manager, err := db.NewManager(dbPath)
	if err != nil {
		log.Fatalf("FATAL: could not initialize VictoriaEngine: %v", err)
	}
	defer manager.Close()

	sqlDB := manager.DB()
	if err := auth.InitTables(sqlDB); err != nil {
		log.Fatalf("FATAL: could not init auth tables: %v", err)
	}
	secret, err := auth.GetOrCreateSecret(sqlDB)
	if err != nil {
		log.Fatalf("FATAL: could not load auth secret: %v", err)
	}

	// --setup: create admin interactively, then exit
	if *setup {
		runSetup(sqlDB)
		return
	}

	// --reset-admin: delete all admins and create a new one non-interactively
	if *resetAdmin {
		if *adminPass == "" {
			log.Fatal("--reset-admin requiere --admin-pass")
		}
		if _, err := sqlDB.Exec(`DELETE FROM _victoria_admins`); err != nil {
			log.Fatalf("Error al eliminar admins: %v", err)
		}
		if err := auth.CreateAdmin(sqlDB, *adminUser, *adminPass); err != nil {
			log.Fatalf("Error al crear admin: %v", err)
		}
		fmt.Printf("✅ Admin '%s' creado con nueva contraseña.\n", *adminUser)
		return
	}

	if has, _ := auth.HasAdmin(sqlDB); !has {
		log.Printf("⚠️  Sin administrador. Ejecuta primero: .\\victoriadb.exe --setup")
	}

	hub := api.NewSSEHub()
	go hub.Run()

	kernel := &VictoriaKernel{
		Manager: manager,
		Hub:     hub,
		Secret:  secret,
		Port:    *port,
		DataDir: *dataDir,
	}

	mux := http.NewServeMux()
	kernel.registerRoutes(mux)

	addr := fmt.Sprintf(":%d", kernel.Port)
	log.Printf("🚀 VictoriaDB is running at http://localhost%s", addr)
	log.Printf("📁 Data directory: %s", kernel.DataDir)

	if err := http.ListenAndServe(addr, core.WithCORS(mux)); err != nil {
		log.Fatalf("FATAL: server error: %v", err)
	}
}

// runSetup prompts for username and password, creates the admin, and exits.
func runSetup(sqlDB *sql.DB) {
	scanner := bufio.NewScanner(os.Stdin)

	fmt.Println("=== VictoriaDB Setup — Crear administrador ===")

	has, _ := auth.HasAdmin(sqlDB)
	if has {
		fmt.Println("Ya existe un administrador. Para cambiar la contraseña, elimina la tabla _victoria_admins.")
		return
	}

	fmt.Print("Usuario: ")
	scanner.Scan()
	username := strings.TrimSpace(scanner.Text())
	if username == "" {
		log.Fatal("El nombre de usuario no puede estar vacío")
	}

	fmt.Print("Contraseña: ")
	// Try to read without echo (Windows)
	password := readPassword(scanner)
	if password == "" {
		log.Fatal("La contraseña no puede estar vacía")
	}

	fmt.Println("\nCreando administrador...")
	if err := auth.CreateAdmin(sqlDB, username, password); err != nil {
		log.Fatalf("Error al crear administrador: %v", err)
	}
	fmt.Printf("✅ Administrador '%s' creado exitosamente.\n", username)
	fmt.Println("Ahora puedes iniciar el servidor con: .\\victoriadb.exe --port 8090")
}

// readPassword reads a password line (hides input on Windows via syscall if available).
func readPassword(scanner *bufio.Scanner) string {
	// Attempt to disable echo on Windows console
	var old uint32
	handle := syscall.Handle(os.Stdin.Fd())
	const ENABLE_ECHO_INPUT = 0x0004
	// kernel32 GetConsoleMode / SetConsoleMode
	procGetConsoleMode := syscall.MustLoadDLL("kernel32.dll").MustFindProc("GetConsoleMode")
	procSetConsoleMode := syscall.MustLoadDLL("kernel32.dll").MustFindProc("SetConsoleMode")
	procGetConsoleMode.Call(uintptr(handle), uintptr(unsafe.Pointer(&old)))
	procSetConsoleMode.Call(uintptr(handle), uintptr(old&^ENABLE_ECHO_INPUT))
	defer func() {
		procSetConsoleMode.Call(uintptr(handle), uintptr(old))
		fmt.Println()
	}()
	scanner.Scan()
	return strings.TrimSpace(scanner.Text())
}

// registerRoutes attaches all API and UI routes to the mux.
func (k *VictoriaKernel) registerRoutes(mux *http.ServeMux) {
	handlers := api.NewHandlers(k.Manager, k.Hub)
	protected := auth.WithAuth(k.Secret)

	// --- Auth endpoints (public) ---
	mux.HandleFunc("POST /api/auth/login", core.Chain(k.handleLogin, core.WithLogging, core.WithErrorRecovery))

	// --- Schema API (protected) ---
	mux.HandleFunc("GET /api/v1/schema", core.Chain(handlers.GetSchema, core.WithLogging, core.WithErrorRecovery, protected))
	mux.HandleFunc("POST /api/v1/schema/tables", core.Chain(handlers.CreateTable, core.WithLogging, core.WithErrorRecovery, protected))
	mux.HandleFunc("PUT /api/v1/schema/tables/{table}", core.Chain(handlers.UpdateTable, core.WithLogging, core.WithErrorRecovery, protected))
	mux.HandleFunc("DELETE /api/v1/schema/tables/{table}", core.Chain(handlers.DropTable, core.WithLogging, core.WithErrorRecovery, protected))
	mux.HandleFunc("POST /api/v1/schema/migrate", core.Chain(handlers.MigrateSchema, core.WithLogging, core.WithErrorRecovery, protected))
	mux.HandleFunc("POST /api/v1/schema/tables/{table}/foreignkeys", core.Chain(handlers.AddForeignKey, core.WithLogging, core.WithErrorRecovery, protected))
	mux.HandleFunc("DELETE /api/v1/schema/tables/{table}/foreignkeys/{column}", core.Chain(handlers.RemoveForeignKey, core.WithLogging, core.WithErrorRecovery, protected))

	// --- Collections (protected) ---
	mux.HandleFunc("GET /api/v1/collections/{table}/records", core.Chain(handlers.ListRecords, core.WithLogging, core.WithErrorRecovery, protected))
	mux.HandleFunc("POST /api/v1/collections/{table}/records", core.Chain(handlers.CreateRecord, core.WithLogging, core.WithErrorRecovery, protected))
	mux.HandleFunc("GET /api/v1/collections/{table}/records/{id}", core.Chain(handlers.GetRecord, core.WithLogging, core.WithErrorRecovery, protected))
	mux.HandleFunc("PUT /api/v1/collections/{table}/records/{id}", core.Chain(handlers.UpdateRecord, core.WithLogging, core.WithErrorRecovery, protected))
	mux.HandleFunc("DELETE /api/v1/collections/{table}/records/{id}", core.Chain(handlers.DeleteRecord, core.WithLogging, core.WithErrorRecovery, protected))

	// --- Real-Time SSE (protected via ?token=) ---
	mux.HandleFunc("GET /api/v1/realtime", core.Chain(k.Hub.ServeSSE, core.WithLogging, core.WithErrorRecovery, protected))

	// --- Static UI ---
	uiFS, err := fs.Sub(embeddedUI, "ui/dist")
	if err != nil {
		log.Printf("WARNING: embedded UI not found: %v", err)
		return
	}
	fileServer := http.FileServer(http.FS(uiFS))
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "/"
		}
		if _, err := fs.Stat(uiFS, path); err != nil {
			r.URL.Path = "/"
		}
		fileServer.ServeHTTP(w, r)
	}))
}

// handleLogin authenticates an admin and returns a JWT token.
func (k *VictoriaKernel) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !core.DecodeJSON(w, r, &req) {
		return
	}
	token, err := auth.Login(k.Manager.DB(), k.Secret, req.Username, req.Password)
	if err != nil {
		core.WriteError(w, http.StatusUnauthorized, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"token": token})
}
