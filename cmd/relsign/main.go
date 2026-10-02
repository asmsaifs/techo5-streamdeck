// Command relsign makes and uses the release signing key.
//
//	relsign keygen <file>            write a new key (base64 seed) to file, print the public half
//	relsign sign <keyfile> <file>    write <file>.sig, the signature of file
//	relsign sums <version> <files…>  print a checksums.txt for the files
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/asmsaifs/techo5-streamdeck/internal/update"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "relsign:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: relsign keygen|sign|sums …")
	}
	switch args[0] {
	case "keygen":
		if len(args) != 2 {
			return fmt.Errorf("usage: relsign keygen <file>")
		}
		seed := make([]byte, ed25519.SeedSize)
		if _, err := rand.Read(seed); err != nil {
			return err
		}
		if err := os.WriteFile(args[1], []byte(base64.StdEncoding.EncodeToString(seed)+"\n"), 0o600); err != nil {
			return err
		}
		pub := ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey)
		fmt.Println(base64.StdEncoding.EncodeToString(pub))
	case "sign":
		if len(args) != 3 {
			return fmt.Errorf("usage: relsign sign <keyfile> <file>")
		}
		seed, err := os.ReadFile(args[1])
		if err != nil {
			return err
		}
		data, err := os.ReadFile(args[2])
		if err != nil {
			return err
		}
		sig, err := update.Sign(data, string(seed))
		if err != nil {
			return err
		}
		return os.WriteFile(args[2]+".sig", []byte(sig), 0o644)
	case "sums":
		if len(args) < 3 {
			return fmt.Errorf("usage: relsign sums <version> <files…>")
		}
		fmt.Printf("techo5-streamdeck %s\n", args[1])
		for _, f := range args[2:] {
			h := sha256.New()
			fh, err := os.Open(f)
			if err != nil {
				return err
			}
			_, err = io.Copy(h, fh)
			fh.Close()
			if err != nil {
				return err
			}
			fmt.Printf("%s  %s\n", hex.EncodeToString(h.Sum(nil)), filepath.Base(f))
		}
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
	return nil
}
