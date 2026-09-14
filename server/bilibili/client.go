// 修改说明（本文件派生自 github.com/iuroc/bilidown，Apache-2.0）：
// 2026-09-14 关闭 HTTP/2 并复用 Transport，修复大文件下载到约 92% 被 CDN 重置的问题。

package bilibili

import (
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"bilidown/util"
)

type BiliClient struct {
	SESSDATA string
}

// httpClient 是全局复用的 HTTP 客户端。
//
// 为什么要显式禁用 HTTP/2：
// 项目原来每次请求都新建 &http.Transport{Proxy: ...}，本意只是禁用代理。但 Go 对
// 手工构造的 Transport 会在「未提供自定义 Dial/TLSClientConfig」时自动尝试 HTTP/2
// （ForceAttemptHTTP2 的零值语义是「自动尝试」，不是「禁用」），于是媒体下载走了 h2。
// B 站 CDN 的 h2 长连接会在传输中途发 RST_STREAM(INTERNAL_ERROR)，表现为
// "stream error: stream ID N; INTERNAL_ERROR; received from peer"，大文件下到一半即失败。
// 把 TLSNextProto 设为非 nil 的空 map 是官方文档指定的关闭 HTTP/2 的方法。
// 参考：golang/go#51323
//
// 全局复用还顺带获得连接复用与 DNS 缓存，批量解析更快。
var httpClient = &http.Client{
	Transport: &http.Transport{
		Proxy: http.ProxyURL(nil),
		// 非 nil 的空 map = 不装配 HTTP/2
		TLSNextProto:        make(map[string]func(string, *tls.Conn) http.RoundTripper),
		MaxIdleConns:        64,
		MaxIdleConnsPerHost: 16,
		IdleConnTimeout:     90 * time.Second,
	},
}

// SimpleGET 简单的 GET 请求
func (client *BiliClient) SimpleGET(_url string, params map[string]string) (*http.Response, error) {
	values := url.Values{}
	for k, v := range params {
		values.Set(k, v)
	}
	// 无参数时不要留下孤立的 "?"，保证请求 URL 与正常预期完全一致
	requestURL := strings.TrimSuffix(_url, "?")
	if query := values.Encode(); query != "" {
		requestURL = requestURL + "?" + query
	}
	request, err := http.NewRequest("GET", requestURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header = client.MakeHeader()
	return httpClient.Do(request)
}

// MakeHeader 生成请求头
func (client *BiliClient) MakeHeader() http.Header {
	header := http.Header{}
	header.Set("Cookie", "SESSDATA="+client.SESSDATA)
	header.Set("User-Agent", "Mozilla/5.0")
	header.Set("Referer", "https://www.bilibili.com")
	return header
}

// CheckLogin 检查是否已经登录
func (client *BiliClient) CheckLogin() (bool, error) {
	response, err := client.SimpleGET("https://api.bilibili.com/x/space/myinfo", nil)
	if err != nil {
		return false, err
	}
	defer response.Body.Close()
	body := BaseResV2{}
	err = json.NewDecoder(response.Body).Decode(&body)
	if err != nil {
		return false, err
	}
	if body.Code != 0 {
		return false, errors.New(body.Message)
	}
	return body.Success(), nil
}

// NewQRInfo 获取登录二维码信息
func (client *BiliClient) NewQRInfo() (*QRInfo, error) {
	response, err := client.SimpleGET("https://passport.bilibili.com/x/passport-login/web/qrcode/generate", nil)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	body := BaseResV2{}
	err = json.NewDecoder(response.Body).Decode(&body)
	if err != nil {
		return nil, err
	}
	if body.Code != 0 {
		return nil, errors.New(body.Message)
	}
	qrInfo := QRInfo{}
	err = json.Unmarshal(body.Data, &qrInfo)
	if err != nil {
		return nil, err
	}
	return &qrInfo, nil
}

func (client *BiliClient) getWbiKeyRemote() (wbiKey string, err error) {
	if client.SESSDATA == "" {
		return "", errors.New("SESSDATA 不能为空")
	}
	response, err := client.SimpleGET("https://api.bilibili.com/x/web-interface/nav", nil)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	body := BaseResV2{}
	if err = json.NewDecoder(response.Body).Decode(&body); err != nil {
		return "", err
	}
	var data struct {
		WbiImg struct {
			ImgURL string `json:"img_url"`
			SubURL string `json:"sub_url"`
		} `json:"wbi_img"`
	}
	if err = json.Unmarshal(body.Data, &data); err != nil {
		return "", err
	}
	match := regexp.MustCompile(`/bfs/wbi/([a-z0-9]+)\.`)
	imgKey := match.FindStringSubmatch(data.WbiImg.ImgURL)[1]
	subKey := match.FindStringSubmatch(data.WbiImg.SubURL)[1]
	if imgKey == "" || subKey == "" {
		return "", errors.New("regexp.MustCompile(`/bfs/wbi/([a-z0-9])\\.`)")
	}
	return imgKey + subKey, nil
}

// GetQRStatus 获取二维码状态
func (client *BiliClient) GetQRStatus(qrKey string) (qrStatus *QRStatus, sessdata string, err error) {
	params := map[string]string{
		"qrcode_key": qrKey,
	}
	response, err := client.SimpleGET("https://passport.bilibili.com/x/passport-login/web/qrcode/poll", params)
	if err != nil {
		return nil, "", err
	}
	defer response.Body.Close()
	body := BaseResV2{}
	err = json.NewDecoder(response.Body).Decode(&body)
	if err != nil {
		return nil, "", err
	}
	if body.Code != 0 {
		return nil, "", errors.New(body.Message)
	}
	qrStatus = &QRStatus{}
	err = json.Unmarshal(body.Data, &qrStatus)
	if err != nil {
		return nil, "", err
	}
	if qrStatus.Code != 0 {
		return qrStatus, "", nil
	}
	sessdata, err = GetCookieValue(response.Cookies(), "SESSDATA")
	if err != nil {
		return nil, "", err
	}
	return qrStatus, sessdata, nil
}

// GetCookieValue 获取指定 Name 的 Cookie 值
func GetCookieValue(cookies []*http.Cookie, name string) (string, error) {
	for _, cookie := range cookies {
		if cookie.Name == name {
			return cookie.Value, nil
		}
	}
	return "", errors.New("cookie with name " + name + " not found")
}

// SaveSessdata 保存 SESSDATA
func SaveSessdata(db *sql.DB, sessdata string) error {
	util.SqliteLock.Lock()
	_, err := db.Exec(`INSERT OR REPLACE INTO "field" ("name", "value") VALUES ("sessdata", ?)`, sessdata)
	util.SqliteLock.Unlock()
	return err
}

// GetSessdata 获取 SESSDATA
func GetSessdata(db *sql.DB) (string, error) {
	util.SqliteLock.Lock()
	row := db.QueryRow(`SELECT "value" FROM "field" WHERE "name" = "sessdata"`)
	util.SqliteLock.Unlock()
	var sessdata string
	err := row.Scan(&sessdata)
	return sessdata, err
}
