package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/fahadjibransheikh/raven/internal/auth"
	"github.com/fahadjibransheikh/raven/internal/config"
	"github.com/fahadjibransheikh/raven/internal/handler"
	"github.com/fahadjibransheikh/raven/internal/httpguard"
	"github.com/fahadjibransheikh/raven/internal/legacyenv"
	"github.com/fahadjibransheikh/raven/internal/mail"
	"github.com/fahadjibransheikh/raven/internal/mailauth"
	"github.com/fahadjibransheikh/raven/internal/notifications"
	"github.com/fahadjibransheikh/raven/internal/runtimeguard"
	"github.com/fahadjibransheikh/raven/internal/storage"
	"github.com/fahadjibransheikh/raven/internal/store"
	"github.com/fahadjibransheikh/raven/internal/views"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	webpush "github.com/SherClockHolmes/webpush-go"
)

// ConfiguredDatabasePath is RAVEN_DB_PATH, or the default data/raven.db. An
// explicit path is used exactly as given. For the default, a database left by
// an install from before the rename (data/raven.db) is renamed into place first.
func ConfiguredDatabasePath() string {
	if dbPath := legacyenv.Get("RAVEN_DB_PATH"); dbPath != "" {
		return dbPath
	}
	if err := adoptLegacyDatabase(defaultDatabasePath); err != nil {
		log.Fatalf("storage: could not move the existing database to its new name: %v", err)
	}
	return defaultDatabasePath
}

// RunShared runs the open and personal modes on the single shared database.
func RunShared() {
	log.Printf("boot: loading configuration")

	httpConfig, err := httpguard.LoadConfig()
	if err != nil {
		log.Fatalf("invalid HTTP configuration: %v", err)
	}
	authConfig := auth.LoadConfig(httpConfig.BaseURL)
	if err := authConfig.ValidateMode(); err != nil {
		log.Fatalf("invalid authentication configuration: %v", err)
	}
	if err := authConfig.ValidateDesktopToken(); err != nil {
		log.Fatalf("invalid authentication configuration: %v", err)
	}
	authConfig.SecureCookies = httpConfig.SecureCookies()
	mailboxOAuthConfig := mailauth.LoadConfig(httpConfig.BaseURL, authConfig.Enabled)
	if err := httpConfig.ValidateExposure(authConfig.Enabled); err != nil {
		log.Fatalf("unsafe HTTP configuration: %v", err)
	}
	if httpConfig.WarnInsecureLAN(authConfig.Enabled) {
		log.Printf("WARNING: RAVEN_ALLOW_INSECURE_LAN is enabled; sign-in cookies for %s travel unencrypted on the local network", httpConfig.BaseURL)
	}
	if httpConfig.WarnUnauthenticatedRemote(authConfig.Enabled) {
		log.Printf("WARNING: unauthenticated remote access is explicitly enabled; anyone who can reach %s can control Raven", httpConfig.BaseURL)
	}

	dbPath := ConfiguredDatabasePath()
	log.Printf("boot: database path resolved to %s", dbPath)

	dataDir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		log.Fatalf("failed to create database directory: %v", err)
	}
	runtimeLock, err := runtimeguard.Acquire(dbPath)
	if err != nil {
		log.Fatalf("failed to acquire exclusive database lock: %v", err)
	}
	defer runtimeLock.Close()
	log.Printf("boot: exclusive database lock acquired")
	if err := storage.VerifySharedStorageRuntime(context.Background(), dbPath); err != nil {
		log.Fatalf("incompatible storage layout: %v", err)
	}

	db, err := storage.New(dbPath)
	if err != nil {
		log.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()
	log.Printf("boot: database opened")

	secretKey := loadOrGenerateSecretKey(filepath.Join(dataDir, "secret.key"))
	log.Printf("boot: encryption key loaded")

	accountStore, err := config.NewAccountStore(db, secretKey)
	if err != nil {
		log.Fatalf("failed to create account store: %v", err)
	}
	log.Printf("boot: account store initialized")

	blobStore := store.NewBlobStore(filepath.Join(dataDir, "accounts"))
	log.Printf("boot: blob store initialized")

	authManager := auth.NewManager(authConfig, db, auth.Dependencies{BucketHashKey: secretKey})
	if err := authManager.ValidateRuntimeMode(context.Background()); err != nil {
		log.Fatalf("incompatible authentication mode: %v", err)
	}
	log.Printf("boot: authentication mode=%s", authConfig.AuthenticationMode())
	var setupNotice bytes.Buffer
	if err := provisionInitialSetupToken(context.Background(), authManager, authConfig.SetupToken, &setupNotice); err != nil {
		log.Fatalf("failed to provision authentication setup token: %v", err)
	}
	mailCredentials := mailauth.New(mailboxOAuthConfig, db, secretKey)
	if err := mailCredentials.SecureOAuthCredentials(context.Background()); err != nil {
		log.Fatalf("failed to secure mailbox OAuth credentials: %v", err)
	}
	log.Printf("boot: mailbox credential service initialized")

	if err := authManager.EnsureDefaultUser(); err != nil {
		log.Fatalf("failed to ensure default user: %v", err)
	}
	log.Printf("boot: default user ensured")
	if err := accountStore.SecurePasswords(context.Background()); err != nil {
		log.Fatalf("failed to secure stored mailbox passwords: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if authManager.IsEnabled() {
		authManager.StartSessionCleanup(ctx)
		mailCredentials.StartCleanup(ctx)
		log.Printf("boot: session cleanup worker started")
	}

	syncer := mail.NewSyncOrchestrator(db, accountStore, blobStore, mailCredentials)
	log.Printf("boot: sync orchestrator initialized")
	vapidPublicKey, vapidPrivateKey := loadOrGenerateVAPIDKeys(filepath.Join(dataDir, "vapid_private.key"), filepath.Join(dataDir, "vapid_public.key"))
	vapidSubject := legacyenv.Get("RAVEN_VAPID_SUBJECT")
	if vapidSubject == "" {
		vapidSubject = "https://github.com/fahadjibransheikh/raven"
	}
	log.Printf("boot: web push VAPID keys loaded")
	notificationService := notifications.New(db, syncer.Events(), vapidPublicKey, vapidPrivateKey, vapidSubject)
	notificationService.Start(ctx)
	log.Printf("boot: notification worker started")

	syncer.StartPush(ctx, mail.LoadPushConfig())

	mux := http.NewServeMux()
	h := handler.New(db, accountStore, syncer, blobStore, authManager, vapidPublicKey, mailCredentials)
	h.SetTestPushSender(notificationService.SendTestPush)

	go func() {
		log.Printf("boot: background threading worker started")
		db.SetThreadingState(storage.ThreadingState{InProgress: true})
		if err := db.EnsureThreading(ctx); err != nil {
			log.Printf("boot: background threading worker failed: %v", err)
			db.SetThreadingState(storage.ThreadingState{InProgress: false})
		} else {
			log.Printf("boot: background threading worker finished")
		}

		log.Printf("boot: sync orchestrator startup launched")
		syncer.Start(ctx)
	}()

	h.StartAvatarBackfill(ctx)
	h.StartContactSync(ctx)
	h.StartCalendarSync(ctx)
	h.StartOutgoingSendWorker(ctx)
	h.StartMessageMutationWorker(ctx)
	h.StartMailRetentionWorker(ctx)
	h.StartAuthenticationEventRetentionWorker(ctx)
	h.RegisterRoutes(mux)
	log.Printf("boot: HTTP routes registered")
	h.StartAccountDeletionCleanup(ctx)

	var handler http.Handler = views.WithMailboxOAuth(mailboxOAuthConfig.GoogleClient != nil, mailboxOAuthConfig.MicrosoftClient != nil, views.WithListViewCookie(mux))
	handler = authManager.Middleware(handler)
	handler = httpConfig.Middleware(handler)
	handler = httpConfig.ClientSourceMiddleware(handler)
	handler = httpConfig.ClientNetworkMiddleware(authConfig.Enabled, handler)
	handler = httpguard.Gzip(handler)

	fmt.Printf("Raven running on %s\n", httpConfig.BaseURL)
	fmt.Printf("listening on %s\n", httpConfig.ListenAddr)
	fmt.Printf("database: %s\n", db.Path())
	if authConfig.Enabled {
		providers := []string{}
		if authManager.HasGoogleLogin() {
			providers = append(providers, "Google")
		}
		if authManager.HasMicrosoftLogin() {
			providers = append(providers, "Microsoft")
		}
		if authManager.HasOIDCLogin() {
			providers = append(providers, authManager.OIDCLoginName())
		}
		appLogin := "none"
		if len(providers) > 0 {
			appLogin = strings.Join(providers, ", ")
		}
		fmt.Printf("auth: %s (application login: %s)\n", authConfig.AuthenticationMode(), appLogin)
	} else {
		fmt.Printf("auth: disabled (local mode)\n")
	}
	// Emit the entire notice together after startup details, rather than burying
	// its one-time secret among initialization messages.
	if _, err := setupNotice.WriteTo(os.Stderr); err != nil {
		log.Fatalf("failed to display setup notice: %v", err)
	}
	server := newHTTPServer(handler)
	server.Addr = httpConfig.ListenAddr
	log.Fatal(server.ListenAndServe())
}

func loadOrGenerateVAPIDKeys(privatePath, publicPath string) (string, string) {
	public, private, err := loadVAPIDKeys(privatePath, publicPath)
	if err != nil {
		log.Fatalf("web push keys: %v", err)
	}
	return public, private
}

func loadVAPIDKeys(privatePath, publicPath string) (string, string, error) {
	if envPrivate, envPublic := legacyenv.Get("RAVEN_VAPID_PRIVATE_KEY"), legacyenv.Get("RAVEN_VAPID_PUBLIC_KEY"); envPrivate != "" && envPublic != "" {
		return envPublic, envPrivate, nil
	}

	privateBytes, privateErr := os.ReadFile(privatePath)
	publicBytes, publicErr := os.ReadFile(publicPath)
	if privateErr == nil && publicErr == nil && len(privateBytes) > 0 && len(publicBytes) > 0 {
		return string(publicBytes), string(privateBytes), nil
	}

	privateKey, publicKey, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		return "", "", fmt.Errorf("generate VAPID keys: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(privatePath), 0700); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(privatePath, []byte(privateKey), 0600); err != nil {
		return "", "", fmt.Errorf("write VAPID private key: %w", err)
	}
	if err := os.WriteFile(publicPath, []byte(publicKey), 0644); err != nil {
		return "", "", fmt.Errorf("write VAPID public key: %w", err)
	}
	log.Printf("generated new VAPID key pair in %s", filepath.Dir(privatePath))
	return publicKey, privateKey, nil
}

func loadOrGenerateSecretKey(path string) []byte {
	if envKey := legacyenv.Get("RAVEN_SECRET_KEY"); envKey != "" {
		key, err := hex.DecodeString(envKey)
		if err != nil || len(key) != 32 {
			log.Fatalf("invalid RAVEN_SECRET_KEY: must be 64 hex characters (32 bytes)")
		}
		return key
	}

	key, err := readOrCreateSecretKey(path)
	if err != nil {
		// Never fall back to a fresh key: every stored mailbox password, TOTP seed
		// and passkey record is encrypted under the existing one.
		log.Fatalf("secret key %s: %v", path, err)
	}
	return key
}

// readOrCreateSecretKey returns the key at path. It creates one only when the
// file does not exist; an unreadable or malformed file is an error, because
// overwriting it would orphan everything encrypted with it.
func readOrCreateSecretKey(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		if len(data) != 32 {
			return nil, fmt.Errorf("expected 32 bytes, found %d; restore it from backup or remove it deliberately to start over", len(data))
		}
		return data, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate secret key: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	// O_EXCL: if another process created it in the meantime, use theirs.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(err, fs.ErrExist) {
		return readOrCreateSecretKey(path)
	}
	if err != nil {
		return nil, err
	}
	if _, err := f.Write(key); err != nil {
		f.Close()
		os.Remove(path)
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	log.Printf("generated new secret key at %s", path)
	return key, nil
}
