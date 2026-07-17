package webapp

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// validateInitData проверяет подпись Telegram WebApp initData и
// возвращает id пользователя.
// https://core.telegram.org/bots/webapps#validating-data-received-via-the-mini-app
func validateInitData(initData, botToken string, maxAge time.Duration) (int64, error) {
	vals, err := url.ParseQuery(initData)
	if err != nil {
		return 0, fmt.Errorf("initdata: parse: %w", err)
	}
	gotHash := vals.Get("hash")
	if gotHash == "" {
		return 0, fmt.Errorf("initdata: нет hash")
	}
	vals.Del("hash")

	keys := make([]string, 0, len(vals))
	for k := range vals {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, k+"="+vals.Get(k))
	}
	checkString := strings.Join(pairs, "\n")

	secret := hmac.New(sha256.New, []byte("WebAppData"))
	secret.Write([]byte(botToken))
	mac := hmac.New(sha256.New, secret.Sum(nil))
	mac.Write([]byte(checkString))
	want := hex.EncodeToString(mac.Sum(nil))
	if subtle.ConstantTimeCompare([]byte(want), []byte(gotHash)) != 1 {
		return 0, fmt.Errorf("initdata: подпись не сходится")
	}

	authDate, err := strconv.ParseInt(vals.Get("auth_date"), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("initdata: auth_date: %w", err)
	}
	if time.Since(time.Unix(authDate, 0)) > maxAge {
		return 0, fmt.Errorf("initdata: протухла (auth_date %d)", authDate)
	}

	var user struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal([]byte(vals.Get("user")), &user); err != nil {
		return 0, fmt.Errorf("initdata: user: %w", err)
	}
	if user.ID == 0 {
		return 0, fmt.Errorf("initdata: нет user.id")
	}
	return user.ID, nil
}
