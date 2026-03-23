package main

import (
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/openSUSE/systemd-mcp/dbus"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

var (
	sockAddr = "/run/gatekeeper/gatekeeper.sock"
	target   = "/var/log/journal"
	actionID = "com.suse.gatekeeper.readlog"
)

func main() {
	pflag.StringVar(&sockAddr, "socket", sockAddr, "socket address to listen on")
	pflag.StringVar(&target, "target", target, "target file or directory which needs the gate keeping")
	pflag.StringVar(&actionID, "action", actionID, "action ID to listen on")
	pflag.Parse()
	viper.SetEnvPrefix("GATEKEEPER")
	viper.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	viper.AutomaticEnv()
	viper.BindPFlags(pflag.CommandLine)

	if err := os.RemoveAll(sockAddr); err != nil {
		log.Fatalf("Failed to remove old socket: %v", err)
	}

	addr, err := net.ResolveUnixAddr("unix", sockAddr)
	if err != nil {
		log.Fatalf("Failed to resolve socket: %v", err)
	}

	l, err := net.ListenUnix("unix", addr)
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}
	defer l.Close()
	defer os.Remove(sockAddr)

	if err := os.Chmod(sockAddr, 0666); err != nil {
		log.Fatalf("Failed to chmod socket: %v", err)
	}

	log.Println("Gatekeeper listening on", sockAddr)

	for {
		conn, err := l.AcceptUnix()
		if err != nil {
			log.Printf("Accept error: %v", err)
			continue
		}
		go handleConnection(conn)
	}
}

func isJournal(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	var header [8]byte
	n, err := f.Read(header[:])
	return err == nil && n == 8 && string(header[:]) == "LPKSHHRH"
}

func handleConnection(conn *net.UnixConn) {
	defer conn.Close()

	file, err := conn.File()
	if err != nil {
		log.Printf("Failed to get file from conn: %v", err)
		return
	}
	defer file.Close()

	fd := int(file.Fd())
	ucred, err := syscall.GetsockoptUcred(fd, syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	if err != nil {
		log.Printf("Failed to get peer credentials: %v", err)
		return
	}
	authorized, err := dbus.CheckPolkitByPID(ucred.Pid, actionID)
	if err != nil {
		log.Printf("Polkit check failed for PID %d: %v", ucred.Pid, err)
		return
	}
	if !authorized {
		log.Printf("Polkit authorization failed for PID %d", ucred.Pid)
		conn.Write([]byte("ERROR: Polkit authorization failed\n"))
		return
	}

	log.Printf("Polkit authorization successful for PID %d. Opening log files:", ucred.Pid)

	var fds []int
	var files []*os.File

	info, err := os.Stat(target)
	if err == nil && info.IsDir() {
		filepath.Walk(target, func(path string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() && strings.HasSuffix(path, ".journal") && isJournal(path) {
				f, err := os.Open(path)
				if err == nil {
					log.Printf("Gatekeeper opening journal file: %s", path)
					files = append(files, f)
					fds = append(fds, int(f.Fd()))
				}
			}
			return nil
		})
	} else if err == nil {
		if strings.HasSuffix(target, ".journal") && isJournal(target) {
			f, err := os.Open(target)
			if err == nil {
				log.Printf("Gatekeeper opening journal file: %s", target)
				files = append(files, f)
				fds = append(fds, int(f.Fd()))
			}
		}
	} else {
		log.Printf("Failed to stat target %s: %v", target, err)
		conn.Write([]byte(fmt.Sprintf("ERROR: %v\n", err)))
		return
	}

	defer func() {
		for _, f := range files {
			f.Close()
		}
	}()

	if len(fds) == 0 {
		log.Printf("No journal files found in %s", target)
		conn.Write([]byte("ERROR: No journal files found\n"))
		return
	}

	rights := syscall.UnixRights(fds...)
	_, _, err = conn.WriteMsgUnix([]byte("OK\n"), rights, nil)
	if err != nil {
		log.Printf("Failed to send FDs: %v", err)
	} else {
		log.Printf("Sent %d FDs successfully", len(fds))
	}
}
