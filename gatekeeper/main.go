package main

import (
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/coreos/go-systemd/v22/activation"
	"github.com/openSUSE/systemd-mcp/dbus"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

var (
	sockAddr = "/run/gatekeeper/gatekeeper.socket"
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

	var l *net.UnixListener

	listeners, err := activation.Listeners()
	if err == nil && len(listeners) > 0 {
		if len(listeners) > 1 {
			log.Fatalf("Too many listeners: %d", len(listeners))
		}
		var ok bool
		l, ok = listeners[0].(*net.UnixListener)
		if !ok {
			log.Fatalf("Listener is not a unix listener")
		}
		log.Println("Gatekeeper using systemd socket activation")
	} else {
		if err := os.RemoveAll(sockAddr); err != nil {
			log.Fatalf("Failed to remove old socket: %v", err)
		}

		addr, err := net.ResolveUnixAddr("unix", sockAddr)
		if err != nil {
			log.Fatalf("Failed to resolve socket: %v", err)
		}

		l, err = net.ListenUnix("unix", addr)
		if err != nil {
			log.Fatalf("Failed to listen: %v", err)
		}

		if err := os.Chmod(sockAddr, 0666); err != nil {
			log.Fatalf("Failed to chmod socket: %v", err)
		}
		log.Println("Gatekeeper listening on", sockAddr)
	}
	defer l.Close()

	for {
		conn, err := l.AcceptUnix()
		if err != nil {
			log.Printf("Accept error: %v", err)
			continue
		}
		go handleConnection(conn)
	}
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

	log.Printf("Polkit authorization successful for PID %d. Opening log files.", ucred.Pid)

	var fds []int
	var files []*os.File

	info, err := os.Stat(target)
	if err == nil && info.IsDir() {
		filepath.Walk(target, func(path string, info os.FileInfo, err error) error {
			if err == nil {
				f, err := os.Open(path)
				if err == nil {
					files = append(files, f)
					fds = append(fds, int(f.Fd()))
				}
			}
			return nil
		})
	} else if err == nil {
		if strings.HasSuffix(target, ".journal") {
			f, err := os.Open(target)
			if err == nil {
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
	}
}
