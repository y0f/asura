package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/y0f/asura/internal/config"
	"github.com/y0f/asura/internal/storage"
	"github.com/y0f/asura/internal/totp"
)

func generateAPIKey() (key, hash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("generate random bytes: %w", err)
	}
	key = "ak_" + hex.EncodeToString(b)
	hash = config.HashAPIKey(key)
	return key, hash, nil
}

func handleTOTPCommands(configPath, setupName, verifyName, removeName string, args []string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}

	ctx := context.Background()
	store, err := storage.Open(ctx, storage.Options{
		Driver: cfg.Database.Driver, Path: cfg.Database.Path, DSN: cfg.Database.DSN,
		MaxReadConns: 1, EncryptionKey: cfg.Database.EncryptionKey,
	})
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer store.Close()

	switch {
	case setupName != "":
		return handleSetupTOTP(ctx, store, cfg, setupName)
	case verifyName != "":
		return handleVerifyTOTP(ctx, store, verifyName, args)
	case removeName != "":
		return handleRemoveTOTP(ctx, store, removeName)
	}
	return nil
}

func handleSetupTOTP(ctx context.Context, store storage.Store, cfg *config.Config, name string) error {
	apiKey := cfg.LookupAPIKeyByName(name)
	if apiKey == nil {
		return fmt.Errorf("API key %q not found in config", name)
	}
	if !apiKey.TOTP {
		return fmt.Errorf("API key %q does not have totp: true in config", name)
	}
	if existing, _ := store.GetTOTPKey(ctx, name); existing != nil {
		return fmt.Errorf("TOTP already configured for %q — remove first with --remove-totp %s", name, name)
	}

	secret, err := totp.GenerateSecret()
	if err != nil {
		return err
	}
	encoded := totp.EncodeSecret(secret)
	if err := store.CreateTOTPKey(ctx, &storage.TOTPKey{APIKeyName: name, Secret: encoded}); err != nil {
		return fmt.Errorf("store TOTP key: %w", err)
	}

	uri := totp.FormatKeyURI("Asura", name, secret)
	fmt.Println()
	fmt.Printf("  TOTP configured for %q\n", name)
	fmt.Println()
	fmt.Printf("  Secret : %s\n", encoded)
	fmt.Printf("  URI    : %s\n", uri)
	fmt.Println()
	printQR(uri)
	fmt.Println("  Scan the QR code or enter the secret in your authenticator app.")
	fmt.Println("  Verify with: asura --verify-totp", name, "CODE")
	fmt.Println()
	return nil
}

func handleVerifyTOTP(ctx context.Context, store storage.Store, name string, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: asura --verify-totp %s CODE", name)
	}
	totpKey, err := store.GetTOTPKey(ctx, name)
	if err != nil {
		return fmt.Errorf("no TOTP key found for %q", name)
	}
	secret, err := totp.DecodeSecret(totpKey.Secret)
	if err != nil {
		return fmt.Errorf("decode TOTP secret: %w", err)
	}
	if totp.Validate(secret, args[0], time.Now()) {
		fmt.Println("valid")
		return nil
	}
	fmt.Println("invalid")
	return fmt.Errorf("TOTP code is invalid")
}

func handleRemoveTOTP(ctx context.Context, store storage.Store, name string) error {
	if err := store.DeleteTOTPKey(ctx, name); err != nil {
		return err
	}
	fmt.Printf("TOTP removed for %q\n", name)
	return nil
}

func printQR(data string) {
	cmd := exec.Command("qrencode", "-t", "ANSIUTF8", data)
	cmd.Stdout = os.Stdout
	if err := cmd.Run(); err != nil {
		fmt.Println("  (install qrencode for a scannable QR code: apt install qrencode)")
		fmt.Println()
	}
}
