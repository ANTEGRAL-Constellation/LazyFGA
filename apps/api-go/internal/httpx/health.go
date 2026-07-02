package httpx

import (
	"net/http"
	"sync"
)

// Health는 /healthz 핸들러 의존성이다. ping 함수와 storeReady 플래그를 주입받는다.
type Health struct {
	Version    string
	DBPing     func(r *http.Request) bool
	FGAPing    func(r *http.Request) bool
	StoreReady func() bool
}

// healthResponse의 필드 순서는 TS 응답과 동일하다: status, version, db, openfga, storeReady.
type healthResponse struct {
	Status     string `json:"status"`
	Version    string `json:"version"`
	DB         string `json:"db"`
	OpenFGA    string `json:"openfga"`
	StoreReady bool   `json:"storeReady"`
}

// Handler는 db·openfga 헬스와 storeReady를 종합해 200/503을 반환한다.
// 라이브니스/레디니스는 미인증 공개(오케스트레이터용)이며 storeId 등 인프라 식별자는 노출하지 않는다.
func (h Health) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// db·fga ping을 병렬 수행(TS Promise.all).
		var dbUp, fgaUp bool
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); dbUp = h.DBPing(r) }()
		go func() { defer wg.Done(); fgaUp = h.FGAPing(r) }()
		wg.Wait()

		ready := h.StoreReady()
		ok := dbUp && fgaUp && ready
		status := http.StatusOK
		statusStr := "ok"
		if !ok {
			status = http.StatusServiceUnavailable
			statusStr = "degraded"
		}
		WriteJSON(w, status, healthResponse{
			Status:     statusStr,
			Version:    h.Version,
			DB:         upDown(dbUp),
			OpenFGA:    upDown(fgaUp),
			StoreReady: ready,
		})
	}
}

func upDown(up bool) string {
	if up {
		return "up"
	}
	return "down"
}
