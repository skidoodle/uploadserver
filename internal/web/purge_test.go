package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"uploadserver/internal"
)

func TestPurgeMediaSecurityAndGracePeriod(t *testing.T) {
	srv, handler, adminSecret := newTestServer(t)
	srv.cfg.PurgeGracePeriod = 24 * time.Hour

	// Create an upload user
	userID, userSecret, err := srv.store.Add("bob", internal.RoleUpload)
	if err != nil {
		t.Fatal(err)
	}

	// Create a dummy file in bob's directory
	userDir := filepath.Join(srv.cfg.Dir, userID)
	if err := os.MkdirAll(userDir, 0o750); err != nil {
		t.Fatal(err)
	}
	testFilePath := filepath.Join(userDir, "image.png")
	if err := os.WriteFile(testFilePath, []byte("hello png"), 0o600); err != nil {
		t.Fatal(err)
	}

	csrf := generateCSRF(userSecret)

	// 1. SSR Purge without confirmation phrase -> fails
	form := url.Values{"_csrf": {csrf}}
	req := httptest.NewRequest("POST", "/_/tokens/"+userID+"/purge-media", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: adminCookieName, Value: userSecret})
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: csrf})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Should redirect with error
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect, got %d", rec.Code)
	}
	if _, ok := srv.store.GetPendingPurge(userID); ok {
		t.Fatal("expected purge to NOT be scheduled without confirmation phrase")
	}

	// 2. SSR Purge with valid confirmation phrase ("PURGE <id>") -> schedules pending purge
	form = url.Values{
		"_csrf":          {csrf},
		"confirm_phrase": {"PURGE " + userID},
	}
	req = httptest.NewRequest("POST", "/_/tokens/"+userID+"/purge-media", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: adminCookieName, Value: userSecret})
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: csrf})
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect, got %d", rec.Code)
	}

	p, ok := srv.store.GetPendingPurge(userID)
	if !ok {
		t.Fatal("expected pending purge to be scheduled in store")
	}
	if p.TokenID != userID {
		t.Errorf("got token ID %q, want %q", p.TokenID, userID)
	}
	// Files should still exist on disk during grace period!
	if _, err := os.Stat(testFilePath); err != nil {
		t.Fatalf("file should still exist on disk during grace period: %v", err)
	}

	// 3. Render Dashboard: pending purge banner should be present
	dashReq := httptest.NewRequest("GET", "/", nil)
	dashReq.AddCookie(&http.Cookie{Name: adminCookieName, Value: userSecret})
	dashRec := httptest.NewRecorder()
	handler.ServeHTTP(dashRec, dashReq)
	dashBody := dashRec.Body.String()
	if !strings.Contains(dashBody, "purge-banner") {
		t.Errorf("expected dashboard to contain purge-banner, body: %s", dashBody)
	}
	if !strings.Contains(dashBody, "Cancel purge") {
		t.Errorf("expected dashboard to contain Cancel purge button")
	}

	// 4. Cancel pending purge via SSR
	cancelForm := url.Values{"_csrf": {csrf}}
	cancelReq := httptest.NewRequest("POST", "/_/tokens/"+userID+"/cancel-purge", strings.NewReader(cancelForm.Encode()))
	cancelReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	cancelReq.AddCookie(&http.Cookie{Name: adminCookieName, Value: userSecret})
	cancelReq.AddCookie(&http.Cookie{Name: csrfCookieName, Value: csrf})
	cancelRec := httptest.NewRecorder()
	handler.ServeHTTP(cancelRec, cancelReq)

	if cancelRec.Code != http.StatusSeeOther {
		t.Fatalf("expected cancel redirect, got %d", cancelRec.Code)
	}

	if _, ok := srv.store.GetPendingPurge(userID); ok {
		t.Fatal("expected pending purge to be cancelled")
	}

	// 5. API purge scheduling and cancel
	apiReqBody := `{"confirm_phrase":"PURGE ` + userID + `"}`
	apiReq := httptest.NewRequest("DELETE", "/_/api/purge", strings.NewReader(apiReqBody))
	apiReq.Header.Set("Authorization", "Bearer "+userSecret)
	apiReq.Header.Set("Content-Type", "application/json")
	apiRec := httptest.NewRecorder()
	handler.ServeHTTP(apiRec, apiReq)

	if apiRec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from API purge, got %d: %s", apiRec.Code, apiRec.Body.String())
	}
	var apiResp map[string]any
	if err := json.Unmarshal(apiRec.Body.Bytes(), &apiResp); err != nil {
		t.Fatal(err)
	}
	if apiResp["scheduled"] != true {
		t.Fatalf("expected scheduled: true in API response, got %v", apiResp)
	}

	// Cancel via API
	cancelAPIReq := httptest.NewRequest("POST", "/_/api/purge/cancel", nil)
	cancelAPIReq.Header.Set("Authorization", "Bearer "+userSecret)
	cancelAPIRec := httptest.NewRecorder()
	handler.ServeHTTP(cancelAPIRec, cancelAPIReq)

	if cancelAPIRec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from cancel API, got %d: %s", cancelAPIRec.Code, cancelAPIRec.Body.String())
	}

	// 6. Admin API purge on behalf of user
	adminPurgeReq := httptest.NewRequest("DELETE", "/_/api/tokens/"+userID+"/media", bytes.NewBufferString(`{"confirm_phrase":"PURGE `+userID+`"}`))
	adminPurgeReq.Header.Set("Authorization", "Bearer "+adminSecret)
	adminPurgeReq.Header.Set("Content-Type", "application/json")
	adminPurgeRec := httptest.NewRecorder()
	handler.ServeHTTP(adminPurgeRec, adminPurgeReq)
	if adminPurgeRec.Code != http.StatusOK {
		t.Fatalf("expected admin purge OK, got %d", adminPurgeRec.Code)
	}

	// Admin cancel API
	adminCancelReq := httptest.NewRequest("POST", "/_/api/tokens/"+userID+"/cancel-purge", nil)
	adminCancelReq.Header.Set("Authorization", "Bearer "+adminSecret)
	adminCancelRec := httptest.NewRecorder()
	handler.ServeHTTP(adminCancelRec, adminCancelReq)
	if adminCancelRec.Code != http.StatusOK {
		t.Fatalf("expected admin cancel OK, got %d", adminCancelRec.Code)
	}
}

func TestPurgeImmediateWhenDisabled(t *testing.T) {
	srv, handler, adminSecret := newTestServer(t)
	srv.cfg.PurgeGracePeriod = 0 // Immediate purge

	userID, userSecret, err := srv.store.Add("charlie", internal.RoleUpload)
	if err != nil {
		t.Fatal(err)
	}

	userDir := filepath.Join(srv.cfg.Dir, userID)
	if err := os.MkdirAll(userDir, 0o750); err != nil {
		t.Fatal(err)
	}
	testFilePath := filepath.Join(userDir, "video.mp4")
	if err := os.WriteFile(testFilePath, []byte("dummy video"), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = srv.store.RecordUploadEntry(userID, internal.UploadEntry{Name: "video.mp4", Size: 11, UploadedAt: time.Now().UTC()})

	// API purge with force / 0s grace period
	apiReqBody := `{"confirm_phrase":"PURGE ` + userID + `"}`
	apiReq := httptest.NewRequest("DELETE", "/_/api/purge", strings.NewReader(apiReqBody))
	apiReq.Header.Set("Authorization", "Bearer "+userSecret)
	apiReq.Header.Set("Content-Type", "application/json")
	apiRec := httptest.NewRecorder()
	handler.ServeHTTP(apiRec, apiReq)

	if apiRec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", apiRec.Code, apiRec.Body.String())
	}
	// Files should be wiped from disk immediately
	if _, err := os.Stat(userDir); !os.IsNotExist(err) {
		t.Errorf("expected user dir to be removed on immediate purge, err: %v", err)
	}

	uploads, _ := srv.store.UploadsFor(userID)
	if len(uploads) != 0 {
		t.Errorf("expected 0 uploads in store, got %d", len(uploads))
	}
	_ = adminSecret
}

func TestPurgeSchedulerExecution(t *testing.T) {
	srv, _, _ := newTestServer(t)

	userID, _, err := srv.store.Add("david", internal.RoleUpload)
	if err != nil {
		t.Fatal(err)
	}

	userDir := filepath.Join(srv.cfg.Dir, userID)
	if err := os.MkdirAll(userDir, 0o750); err != nil {
		t.Fatal(err)
	}
	testFilePath := filepath.Join(userDir, "file.txt")
	if err := os.WriteFile(testFilePath, []byte("text data"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Schedule purge in the past
	_, err = srv.store.SchedulePurge(userID, userID, -1*time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	sched := internal.NewPurgeScheduler(srv.store, func(tokenID string) error {
		return srv.purgeUserMedia(tokenID, true)
	})

	count, err := sched.ProcessNow()
	if err != nil {
		t.Fatalf("ProcessNow: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 purge executed, got %d", count)
	}

	if _, ok := srv.store.GetPendingPurge(userID); ok {
		t.Error("expected pending purge to be removed after execution")
	}

	if _, err := os.Stat(userDir); !os.IsNotExist(err) {
		t.Errorf("expected userDir to be deleted after scheduled purge ran")
	}
}

func TestPurgeUIRenderingBanners(t *testing.T) {
	srv, handler, adminSecret := newTestServer(t)

	userID, userSecret, err := srv.store.Add("elena", internal.RoleUpload)
	if err != nil {
		t.Fatal(err)
	}

	// Schedule purge
	_, err = srv.store.SchedulePurge(userID, userID, 12*time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	// 1. Uploads Page: shows purge banner and does NOT show "Purge All Media" button
	uploadsReq := httptest.NewRequest("GET", "/_/uploads/"+userID, nil)
	uploadsReq.AddCookie(&http.Cookie{Name: adminCookieName, Value: userSecret})
	uploadsRec := httptest.NewRecorder()
	handler.ServeHTTP(uploadsRec, uploadsReq)
	uploadsBody := uploadsRec.Body.String()
	if !strings.Contains(uploadsBody, "purge-banner") {
		t.Errorf("uploads page missing purge-banner: %s", uploadsBody)
	}
	if strings.Contains(uploadsBody, "Purge All Media") {
		t.Errorf("uploads page should not show 'Purge All Media' when purge is pending")
	}

	// 2. User Profile Page (as Admin): shows purge banner and does NOT show "Purge All Media" button
	profReq := httptest.NewRequest("GET", "/_/user/"+userID, nil)
	profReq.AddCookie(&http.Cookie{Name: adminCookieName, Value: adminSecret})
	profRec := httptest.NewRecorder()
	handler.ServeHTTP(profRec, profReq)
	profBody := profRec.Body.String()
	if !strings.Contains(profBody, "purge-banner") {
		t.Errorf("user profile missing purge-banner: %s", profBody)
	}
	if strings.Contains(profBody, "Purge All Media") {
		t.Errorf("user profile page should not show 'Purge All Media' when purge is pending")
	}

	// 3. Dashboard (as Elena): shows purge banner and does NOT show "Purge All Media" button
	dashReq := httptest.NewRequest("GET", "/", nil)
	dashReq.AddCookie(&http.Cookie{Name: adminCookieName, Value: userSecret})
	dashRec := httptest.NewRecorder()
	handler.ServeHTTP(dashRec, dashReq)
	dashBody := dashRec.Body.String()
	if !strings.Contains(dashBody, "purge-banner") {
		t.Errorf("dashboard missing purge-banner: %s", dashBody)
	}
	if strings.Contains(dashBody, "Purge All Media") {
		t.Errorf("dashboard should not show 'Purge All Media' when purge is pending")
	}

	// 4. Duplicate purge via API returns 409 Conflict
	dupReq := httptest.NewRequest("DELETE", "/_/api/purge", strings.NewReader(`{"confirm_phrase":"PURGE `+userID+`"}`))
	dupReq.Header.Set("Authorization", "Bearer "+userSecret)
	dupReq.Header.Set("Content-Type", "application/json")
	dupRec := httptest.NewRecorder()
	handler.ServeHTTP(dupRec, dupReq)
	if dupRec.Code != http.StatusConflict {
		t.Errorf("expected 409 Conflict on duplicate API purge, got %d", dupRec.Code)
	}

	// 5. Users List Page (as Admin): ONLY Elena has class "purging"
	frankID, _, err := srv.store.Add("frank", internal.RoleUpload)
	if err != nil {
		t.Fatal(err)
	}

	usersReq := httptest.NewRequest("GET", "/_/users", nil)
	usersReq.AddCookie(&http.Cookie{Name: adminCookieName, Value: adminSecret})
	usersRec := httptest.NewRecorder()
	handler.ServeHTTP(usersRec, usersReq)
	usersBody := usersRec.Body.String()

	// Elena has purging class and "active, purging" status
	if !strings.Contains(usersBody, "purging") {
		t.Errorf("users list missing purging class: %s", usersBody)
	}
	if !strings.Contains(usersBody, "active, purging") {
		t.Errorf("users list missing 'active, purging' status: %s", usersBody)
	}

	// Cancel Elena's purge and re-render users list
	_, _ = srv.store.CancelPendingPurge(userID)
	usersReq2 := httptest.NewRequest("GET", "/_/users", nil)
	usersReq2.AddCookie(&http.Cookie{Name: adminCookieName, Value: adminSecret})
	usersRec2 := httptest.NewRecorder()
	handler.ServeHTTP(usersRec2, usersReq2)
	usersBody2 := usersRec2.Body.String()

	// Neither Elena nor Frank should have purging now
	if strings.Contains(usersBody2, "purging") {
		t.Errorf("users list should not contain purging class when no purges are pending: %s", usersBody2)
	}
	_ = frankID
}
