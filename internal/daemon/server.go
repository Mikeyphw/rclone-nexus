package daemon

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"sync"
	"syscall"
	"time"

	"rclone-nexus/internal/control"
	"rclone-nexus/internal/paths"
	"rclone-nexus/internal/protocol"
)

type Server struct {
	Paths  paths.Paths
	Engine *control.Engine

	listener  net.Listener
	lockFile  *os.File
	closeOnce sync.Once
	wg        sync.WaitGroup
}

func New(p paths.Paths, engine *control.Engine) *Server {
	return &Server{Paths: p, Engine: engine}
}

func (s *Server) acquireLock() error {
	if err := s.Paths.EnsureState(); err != nil {
		return err
	}
	file, err := os.OpenFile(s.Paths.DaemonLock, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		return fmt.Errorf("RNX_E_DAEMON_ALREADY_RUNNING: %w", err)
	}
	if err := file.Truncate(0); err != nil {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
		return err
	}
	if _, err := file.WriteString(strconv.Itoa(os.Getpid()) + "\n"); err != nil {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
		return err
	}
	_ = file.Chmod(0o600)
	s.lockFile = file
	return nil
}

func (s *Server) Listen() error {
	if err := s.acquireLock(); err != nil {
		return err
	}
	_ = os.Remove(s.Paths.Socket)
	listener, err := net.Listen("unix", s.Paths.Socket)
	if err != nil {
		s.releaseLock()
		return err
	}
	if err := os.Chmod(s.Paths.Socket, 0o600); err != nil {
		_ = listener.Close()
		s.releaseLock()
		return err
	}
	s.listener = listener
	return nil
}

func (s *Server) Serve(ctx context.Context) error {
	if s.listener == nil {
		if err := s.Listen(); err != nil {
			return err
		}
	}
	go func() {
		<-ctx.Done()
		_ = s.Close()
	}()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				break
			}
			return err
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer conn.Close()
			s.serveConn(ctx, conn)
		}()
	}
	s.wg.Wait()
	return nil
}

func (s *Server) serveConn(ctx context.Context, conn net.Conn) {
	_ = conn.SetDeadline(time.Now().Add(24 * time.Hour))
	reader := bufio.NewReaderSize(io.LimitReader(conn, protocol.MaxRequestBytes+2), 64<<10)
	line, err := reader.ReadBytes('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		s.writeDecodeError(conn, protocol.Error("invalid_request", "could not read daemon request", err.Error()))
		return
	}
	if len(line) > protocol.MaxRequestBytes {
		s.writeDecodeError(conn, protocol.Error("payload_too_large", "request exceeds protocol limit", ""))
		return
	}
	request, machineError := protocol.DecodeRequest(bytesReader(line))
	if machineError != nil {
		s.writeDecodeError(conn, machineError)
		return
	}
	sequence := 0
	emit := func(event, message string, data any) {
		sequence++
		payload, marshalErr := protocol.MarshalEvent(protocol.Event{
			SchemaVersion: protocol.SchemaVersion,
			Kind:          "event", RequestID: request.RequestID,
			Protocol: protocolVersion(request), Sequence: sequence,
			Event: event, Message: message, Data: data,
		})
		if marshalErr == nil {
			_ = protocol.WriteNDJSON(conn, payload)
		}
	}
	response := s.Engine.Execute(ctx, request, emit)
	payload, marshalErr := protocol.MarshalResponse(response)
	if marshalErr != nil {
		return
	}
	_ = protocol.WriteNDJSON(conn, payload)
}

func (s *Server) writeDecodeError(writer io.Writer, machineError *protocol.MachineError) {
	response := protocol.Response{SchemaVersion: protocol.SchemaVersion, Kind: "response", RequestID: "invalid", OK: false, Error: machineError}
	payload, err := protocol.MarshalResponse(response)
	if err == nil {
		_ = protocol.WriteNDJSON(writer, payload)
	}
}

func (s *Server) Close() error {
	var result error
	s.closeOnce.Do(func() {
		if s.listener != nil {
			result = s.listener.Close()
		}
		_ = os.Remove(s.Paths.Socket)
		s.releaseLock()
	})
	return result
}

func (s *Server) releaseLock() {
	if s.lockFile != nil {
		_ = syscall.Flock(int(s.lockFile.Fd()), syscall.LOCK_UN)
		_ = s.lockFile.Close()
		s.lockFile = nil
	}
}

type sliceReader struct {
	data   []byte
	offset int
}

func bytesReader(data []byte) *sliceReader { return &sliceReader{data: data} }
func (r *sliceReader) Read(p []byte) (int, error) {
	if r.offset >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.offset:])
	r.offset += n
	return n, nil
}
