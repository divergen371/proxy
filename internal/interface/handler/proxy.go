package handler

import (
	"fmt"
	"net"
	"net/http"
	"strings"

	"proxy/internal/domain"
	"proxy/internal/usecase"
)

type ProxyHandler struct {
	proxyUseCase *usecase.ProxyUseCase
	logger       domain.Logger
}

func NewProxyHandler(
	proxyUseCase *usecase.ProxyUseCase, logger domain.Logger,
) *ProxyHandler {
	return &ProxyHandler{
		proxyUseCase: proxyUseCase,
		logger:       logger,
	}
}

func (h *ProxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodConnect {
		h.logger.Info("Non-CONNECT method received", map[string]interface{}{
			"method": r.Method,
			"url":    r.URL.String(),
		})
		http.Error(w, "Only CONNECT method is supported", http.StatusMethodNotAllowed)
		return
	}

	h.handleConnect(w, r)
}

func (h *ProxyHandler) handleConnect(w http.ResponseWriter, r *http.Request) {
	// アクセス制御チェック
	clientIP := r.RemoteAddr
	if host, _, err := net.SplitHostPort(clientIP); err == nil {
		clientIP = host
	}

	host := r.Host
	if idx := strings.Index(host, ":"); idx != -1 {
		host = host[:idx]
	}

	allowed, err := h.proxyUseCase.CheckAccess(r.Context(), clientIP, host)
	if err != nil {
		h.logger.Error("Access control check failed", err, map[string]interface{}{
			"client_ip": clientIP,
			"host":      host,
		})
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	if !allowed {
		h.logger.Info("Access blocked", map[string]interface{}{
			"client_ip": clientIP,
			"host":      host,
		})
		http.Error(w, fmt.Sprintf("Access to %s is blocked", host), http.StatusForbidden)
		return
	}

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		h.logger.Error("Hijacking not supported", nil, nil)
		http.Error(w, "Hijacking not supported", http.StatusInternalServerError)
		return
	}

	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		h.logger.Error("Hijacking failed", err, nil)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer clientConn.Close()

	// 重要: 200 Connection Established レスポンスを送信
	response := []byte("HTTP/1.1 200 Connection Established\r\n\r\n")
	if _, err := clientConn.Write(response); err != nil {
		h.logger.Error("Failed to write connection established response", err, nil)
		return
	}

	// トンネリングの処理
	if err := h.proxyUseCase.HandleTunnel(r.Context(), clientConn, r.Host, clientIP); err != nil {
		h.logger.Error("Tunnel handling failed", err, map[string]interface{}{
			"host": r.Host,
		})
	}
}
