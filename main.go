package main

import (
    "fmt"
    "log"
    "os"
    "strconv"
    "strings"
    "time"

    "github.com/valyala/fasthttp"
)

var (
    timeoutSec = mustAtoi(os.Getenv("TIMEOUT"), 10)   // default 10s
    retries    = mustAtoi(os.Getenv("RETRIES"), 3)    // default 3 retries
    port       = os.Getenv("PORT")
    proxyKey   = os.Getenv("KEY")
    client     *fasthttp.Client
)

func mustAtoi(s string, fallback int) int {
    if v, err := strconv.Atoi(s); err == nil {
        return v
    }
    return fallback
}

func main() {
    if port == "" {
        port = "8080"
    }
    log.Printf("🚀 starting RoProxy Lite on port %s (timeout=%ds, retries=%d)\n", port, timeoutSec, retries)

    client = &fasthttp.Client{
        ReadTimeout:         time.Duration(timeoutSec) * time.Second,
        MaxIdleConnDuration: 60 * time.Second,
    }

    err := fasthttp.ListenAndServe(":"+port, requestHandler)
    if err != nil {
        log.Fatalf("fatal ListenAndServe error: %v\n", err)
    }
}

func requestHandler(ctx *fasthttp.RequestCtx) {
    defer func() {
        if r := recover(); r != nil {
            log.Printf("⚠️ panic recovered: %v\n", r)
            ctx.SetStatusCode(500)
            ctx.SetBody([]byte(fmt.Sprintf("internal error: %v", r)))
        }
    }()

    log.Printf("%s %s\n", ctx.Method(), ctx.Request.URI().String())

    // auth
    if proxyKey != "" {
        key := string(ctx.Request.Header.Peek("PROXYKEY"))
        if key != proxyKey {
            ctx.SetStatusCode(407)
            ctx.SetBody([]byte("Missing or invalid PROXYKEY"))
            return
        }
    }

    uri := string(ctx.Request.URI().Path())
    parts := strings.SplitN(strings.TrimPrefix(uri, "/"), "/", 2)
    if len(parts) < 2 {
        log.Println("❌ invalid path:", uri)
        ctx.SetStatusCode(400)
        ctx.SetBody([]byte("URL format invalid. use /subdomain/path"))
        return
    }

    resp := makeRequest(ctx, parts, 1)
    defer fasthttp.ReleaseResponse(resp)

    // mirror headers & status
    ctx.SetStatusCode(resp.StatusCode())
    resp.Header.VisitAll(func(key, value []byte) {
        ctx.Response.Header.SetBytesKV(key, value)
    })
    ctx.SetBody(resp.Body())
}

func makeRequest(ctx *fasthttp.RequestCtx, parts []string, attempt int) *fasthttp.Response {
    if attempt > retries {
        r := fasthttp.AcquireResponse()
        r.SetStatusCode(500)
        r.SetBody([]byte("proxy: exceeded retry limit"))
        return r
    }

    subdomain, path := parts[0], parts[1]
    upstream := fmt.Sprintf("https://%s.roblox.com/%s", subdomain, path)
    log.Printf("→ upstream request: %s %s (attempt %d)\n", ctx.Method(), upstream, attempt)

    req := fasthttp.AcquireRequest()
    defer fasthttp.ReleaseRequest(req)
    req.Header.SetMethod(string(ctx.Method()))
    req.SetRequestURI(upstream)
    req.SetBody(ctx.Request.Body())

    // copy headers
    ctx.Request.Header.VisitAll(func(k, v []byte) {
        req.Header.SetBytesKV(k, v)
    })
    // spoof UA
    req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64)")
    req.Header.Set("Accept", "application/json")
    req.Header.Del("Roblox-Id")

    resp := fasthttp.AcquireResponse()
    if err := client.Do(req, resp); err != nil {
        log.Printf("❌ upstream error: %v\n", err)
        fasthttp.ReleaseResponse(resp)
        return makeRequest(ctx, parts, attempt+1)
    }

    log.Printf("← upstream status: %d\n", resp.StatusCode())
    return resp
}
