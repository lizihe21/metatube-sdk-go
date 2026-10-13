package sitehtml

import (
 "context"
 "encoding/json"
 "errors"
 "net/http"
 "net/http/httptest"
 "sync/atomic"
 "testing"
 "time"

 "golang.org/x/text/language"

 "github.com/metatube-community/metatube-sdk-go/internal/envconfig"
 "github.com/metatube-community/metatube-sdk-go/internal/flaresolverr"
 "github.com/metatube-community/metatube-sdk-go/provider"
)

func solverConfig(endpoint, mode string) *envconfig.Config {
 cfg := envconfig.NewConfig()
 cfg.Set("flaresolverr_url", endpoint)
 cfg.Set("flaresolverr_timeout", "5s")
 if mode != "" { cfg.Set("flaresolverr_mode", mode) }
 return cfg
}

func fakeSolver(t *testing.T, html, agent string, cookie bool, responseURL string) (*httptest.Server, *atomic.Int32) {
 t.Helper()
 calls := &atomic.Int32{}
 server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
  calls.Add(1)
  if r.URL.Path != "/v1" || r.Method != http.MethodPost { t.Errorf("wrong API endpoint: %v", r.URL) }
  var payload struct {Command string `json:"cmd"`; URL string `json:"url"`}
  if err := json.NewDecoder(r.Body).Decode(&payload); err != nil { t.Error(err) }
  if payload.Command != "request.get" { t.Errorf("wrong cmd: %q", payload.Command) }
  url := payload.URL
  if responseURL != "" { url = responseURL }
  cookies := []map[string]any{}
  if cookie { cookies = append(cookies,map[string]any{"name":"cf_clearance","value":"ok","path":"/","secure":false}) }
  _ = json.NewEncoder(w).Encode(map[string]any{
   "status":"ok",
   "solution":map[string]any{"url":url,"status":200,"response":html,"userAgent":agent,"cookies":cookies},
  })
 }))
 return server,calls
}

func TestConfiguredSolverFallbackOnChallenge403(t *testing.T) {
 var direct atomic.Int32
 origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request){
  direct.Add(1)
  w.Header().Set("Cf-Mitigated","challenge")
  w.WriteHeader(http.StatusForbidden)
  _,_ = w.Write([]byte("<html><title>Just a moment...</title></html>"))
 }))
 defer origin.Close()
 solver, calls := fakeSolver(t, "<html><div id='video_info'>solved</div></html>", "UA-FS", false, "")
 defer solver.Close()

 client := New("Test",origin.URL,1,language.English,provider.RequestPolicyConfig{MaxConcurrency:1,MaxWait:time.Second})
 if err:=client.SetConfig(solverConfig(solver.URL,"")); err != nil {t.Fatal(err)}
 for i:=0;i<2;i++ {
  doc,finalURL,err:=client.Get(context.Background(),origin.URL+"/detail")
  if err!=nil || finalURL!=origin.URL+"/detail" || doc.Find("#video_info").Length()!=1 {
   t.Fatalf("solver fallback failed: url=%s err=%v", finalURL,err)
  }
 }
 if direct.Load()!=1 || calls.Load()!=2 {
  t.Fatalf("unexpected direct/solver count: %d/%d",direct.Load(),calls.Load())
 }
}

func TestConfiguredSolverAlwaysModeAvoidsDirectRequests(t *testing.T) {
 var direct atomic.Int32
 origin:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){direct.Add(1);w.WriteHeader(200)}))
 defer origin.Close()
 solver,calls:=fakeSolver(t,"<html><div id='video_info'>ready</div></html>","UA-FS",false,"")
 defer solver.Close()
 client:=New("Test",origin.URL,1,language.English,provider.RequestPolicyConfig{MaxConcurrency:1,MaxWait:time.Second})
 if err:=client.SetConfig(solverConfig(solver.URL,"always"));err!=nil{t.Fatal(err)}
 _,_,err:=client.Get(context.Background(),origin.URL+"/movie")
 if err!=nil||direct.Load()!=0||calls.Load()!=1 {t.Fatalf("always mode: err=%v direct=%d solver=%d",err,direct.Load(),calls.Load())}
}

func TestSolverCookiesAndAgentReusedByDirectRequests(t *testing.T) {
 var direct atomic.Int32
 origin:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  n:=direct.Add(1)
  if n==1 {w.Header().Set("Cf-Mitigated","challenge");w.WriteHeader(403);return}
  cookie,err:=r.Cookie("cf_clearance")
  if err!=nil||cookie.Value!="ok"||r.Header.Get("User-Agent")!="UA-FS" {
   t.Errorf("browser session not reused: ua=%q cookie=%v (%v)",r.Header.Get("User-Agent"),cookie,err)
  }
  _,_ = w.Write([]byte("<html><div id='video_info'>direct</div></html>"))
 }))
 defer origin.Close()
 solver,_:=fakeSolver(t,"<html><div id='video_info'>solved</div></html>","UA-FS",true,"")
 defer solver.Close()
 client:=New("Test",origin.URL,1,language.English,provider.RequestPolicyConfig{MaxConcurrency:1,MaxWait:time.Second})
 if err:=client.SetConfig(solverConfig(solver.URL,""));err!=nil{t.Fatal(err)}
 _,_,err:=client.Get(context.Background(),origin.URL+"/detail")
 if err!=nil{t.Fatal(err)}
 client.mu.Lock()
 client.preferSolverUntil=time.Time{} // emulate end of site access cooldown
 client.mu.Unlock()
 doc,_,err:=client.Get(context.Background(),origin.URL+"/detail")
 if err!=nil||doc.Find("#video_info").Text()!="direct"||direct.Load()!=2 {
  t.Fatalf("direct reuse failed: %v requests=%d",err,direct.Load())
 }
}

func TestSolverRejectsCrossOriginSolutionAndDoesNotAccept403(t *testing.T) {
 origin:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  w.Header().Set("Cf-Mitigated","challenge")
  w.WriteHeader(http.StatusForbidden)
 }))
 defer origin.Close()
 solver,_:=fakeSolver(t,"<html>bad</html>","UA-FS",false,"https://attacker.invalid/admin")
 defer solver.Close()
 client:=New("Test",origin.URL,1,language.English,provider.RequestPolicyConfig{MaxConcurrency:1,MaxWait:time.Second})
 if err:=client.SetConfig(solverConfig(solver.URL,""));err!=nil{t.Fatal(err)}
 _,_,err:=client.Get(context.Background(),origin.URL+"/detail")
 if !errors.Is(err,provider.ErrInvalidURL) {t.Fatalf("accepted foreign URL: %v",err)}
}

func TestOrdinary403DoesNotTriggerSolver(t *testing.T) {
 origin:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){w.WriteHeader(http.StatusForbidden)}))
 defer origin.Close()
 solver,calls:=fakeSolver(t,"<html>irrelevant</html>","",false,"")
 defer solver.Close()
 client:=New("Test",origin.URL,1,language.English,provider.RequestPolicyConfig{MaxConcurrency:1,MaxWait:time.Second})
 if err:=client.SetConfig(solverConfig(solver.URL,""));err!=nil{t.Fatal(err)}
 _,_,err:=client.Get(context.Background(),origin.URL+"/detail")
 if !errors.Is(err,provider.ErrAccessDenied)||calls.Load()!=0 {t.Fatalf("wrong fallback: %v solver=%d",err,calls.Load())}
}

func TestSolverConfigRejectsInvalidSettings(t *testing.T) {
 for _,tc:=range []struct{endpoint, mode, timeout string}{
  {"file:///etc/passwd","fallback","5s"},
  {"http://localhost:8191","unsupported","5s"},
  {"http://localhost:8191","always","not-duration"},
 } {
  client:=New("Test","https://example.com",1,language.English,provider.RequestPolicyConfig{MaxConcurrency:1})
  cfg:=envconfig.NewConfig()
  cfg.Set("flaresolverr_url",tc.endpoint)
  cfg.Set("flaresolverr_mode",tc.mode)
  cfg.Set("flaresolverr_timeout",tc.timeout)
  if err:=client.SetConfig(cfg);err==nil {t.Errorf("accepted invalid config: %+v",tc)}
 }
}

func TestSolverErrorRemainsDiscoverable(t *testing.T) {
 origin:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  w.Header().Set("Cf-Mitigated","challenge")
  w.WriteHeader(http.StatusForbidden)
 }))
 defer origin.Close()
 solver:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  _=json.NewEncoder(w).Encode(map[string]string{"status":"error","message":"solver unavailable"})
 }))
 defer solver.Close()
 client:=New("Test",origin.URL,1,language.English,provider.RequestPolicyConfig{MaxConcurrency:1})
 if err:=client.SetConfig(solverConfig(solver.URL,""));err!=nil {t.Fatal(err)}
 _,_,err:=client.Get(context.Background(),origin.URL+"/detail")
 if !errors.Is(err,flaresolverr.ErrService) {t.Fatalf("unexpected solver error: %v",err)}
}
