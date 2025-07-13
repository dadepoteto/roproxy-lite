package main

import (
	"log"
	"time"
	"os"
	"github.com/valyala/fasthttp"
	"strconv"
	"strings"
)

var timeout, _ = strconv.Atoi(os.Getenv("TIMEOUT"))
var retries, _ = strconv.Atoi(os.Getenv("RETRIES"))
var port = os.Getenv("PORT")

var client *fasthttp.Client

func main() {
	h := requestHandler

	client = &fasthttp.Client{
		ReadTimeout:         time.Duration(timeout) * time.Second,
		MaxIdleConnDuration: 60 * time.Second,
	}

	if port == "" {
		port = "8080" // fallback for Railway
	}

	if err := fasthttp.ListenAndServe(":"+port, h); err != nil {
		log.Fatalf("Error in ListenAndServe: %s", err)
	}
}

func requestHandler(ctx *fasthttp.RequestCtx) {
	val, ok := os.LookupEnv("KEY")

	if ok && string(ctx.Request.Header.Peek("PROXYKEY")) != val {
		ctx.SetStatusCode(407)
		ctx.SetBody([]byte("Missing or invalid PROXYKEY header."))
		return
	}

	pathParts := strings.SplitN(string(ctx.Request.Header.RequestURI())[1:], "/", 2)
	if len(pathParts) < 2 {
		ctx.SetStatusCode(400)
		ctx.SetBody([]byte("URL format invalid. Expected /subdomain/path"))
		return
	}

	response := makeRequest(ctx, pathParts, 1)
	defer fasthttp.ReleaseResponse(response)

	ctx.SetStatusCode(response.StatusCode())
	ctx.SetBody(response.Body())
	response.Header.VisitAll(func(key, value []byte) {
		ctx.Response.Header.Set(string(key), string(value))
	})
}

func makeRequest(ctx *fasthttp.RequestCtx, pathParts []string, attempt int) *fasthttp.Response {
	if attempt > retries {
		resp := fasthttp.AcquireResponse()
		resp.SetBody([]byte("Proxy failed to connect. Please try again."))
		resp.SetStatusCode(500)
		return resp
	}

	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)

	subdomain := pathParts[0]
	path := pathParts[1]

	fullURL := "https://" + subdomain + ".roblox.com/" + path
	req.SetRequestURI(fullURL)
	req.Header.SetMethod(string(ctx.Method()))
	req.SetBody(ctx.Request.Body())

	// copy headers from client request to proxy request
	ctx.Request.Header.VisitAll(func(key, value []byte) {
		req.Header.Set(string(key), string(value))
	})

	// spoofing to avoid being blocked
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64)")
	req.Header.Set("Accept", "application/json")
	req.Header.Del("Roblox-Id") // remove any Roblox-Id just in case

	resp := fasthttp.AcquireResponse()
	err := client.Do(req, resp)

	if err != nil {
		fasthttp.ReleaseResponse(resp)
		return makeRequest(ctx, pathParts, attempt+1)
	}

	return resp
}
