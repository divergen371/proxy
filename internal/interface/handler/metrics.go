package handler

import (
	"encoding/json"
	"net/http"

	"proxy/internal/domain"
	"proxy/internal/usecase"
)

// MetricsHandler はメトリクス関連のHTTPリクエストを処理
type MetricsHandler struct {
	metricsUseCase *usecase.MetricsUseCase
	logger         domain.Logger
}

// NewMetricsHandler は新しいMetricsHandlerインスタンスを作成
func NewMetricsHandler(
	metricsUseCase *usecase.MetricsUseCase, logger domain.Logger,
) *MetricsHandler {
	return &MetricsHandler{
		metricsUseCase: metricsUseCase,
		logger:         logger,
	}
}

// HandleMetrics はPrometheus形式のメトリクスを提供
func (h *MetricsHandler) HandleMetrics(w http.ResponseWriter, r *http.Request) {
	metrics, err := h.metricsUseCase.GetPrometheusMetrics(r.Context())
	if err != nil {
		h.logger.Error("Failed to get metrics", err, nil)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(metrics))
}

// HandleStats はJSON形式の詳細な統計情報を提供
func (h *MetricsHandler) HandleStats(w http.ResponseWriter, _ *http.Request) {
	snapshot, err := h.metricsUseCase.GetMetricsSnapshot()
	if err != nil {
		h.logger.Error("Failed to get metrics snapshot", err, nil)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(snapshot); err != nil {
		h.logger.Error("Failed to encode metrics", err, nil)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
}

// HandleHealth はヘルスチェックエンドポイントを提供
func (h *MetricsHandler) HandleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status": "up",
	})
}
