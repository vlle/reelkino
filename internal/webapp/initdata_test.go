package webapp

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"
)

const testToken = "123456:TEST-TOKEN"

// sign строит initData так же, как Telegram: сортированный
// data_check_string + HMAC(secret=HMAC("WebAppData", token)).
func sign(t *testing.T, fields map[string]string) string {
	t.Helper()
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, k+"="+fields[k])
	}
	secret := hmac.New(sha256.New, []byte("WebAppData"))
	secret.Write([]byte(testToken))
	mac := hmac.New(sha256.New, secret.Sum(nil))
	mac.Write([]byte(strings.Join(pairs, "\n")))
	hash := hex.EncodeToString(mac.Sum(nil))

	q := url.Values{}
	for k, v := range fields {
		q.Set(k, v)
	}
	q.Set("hash", hash)
	return q.Encode()
}

func validFields(authDate int64) map[string]string {
	return map[string]string{
		"auth_date": fmt.Sprintf("%d", authDate),
		"user":      `{"id":1919118841,"first_name":"aape"}`,
		"query_id":  "AAF-test",
	}
}

func TestValidateInitData(t *testing.T) {
	nowUnix := time.Now().Unix()

	t.Run("valid", func(t *testing.T) {
		id, err := validateInitData(sign(t, validFields(nowUnix)), testToken, 24*time.Hour)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if id != 1919118841 {
			t.Fatalf("got id %d", id)
		}
	})

	t.Run("tampered user", func(t *testing.T) {
		data := sign(t, validFields(nowUnix))
		data = strings.Replace(data, "1919118841", "1", 1)
		if _, err := validateInitData(data, testToken, 24*time.Hour); err == nil {
			t.Fatal("ожидали ошибку подписи")
		}
	})

	t.Run("wrong token", func(t *testing.T) {
		if _, err := validateInitData(sign(t, validFields(nowUnix)), "other:token", 24*time.Hour); err == nil {
			t.Fatal("ожидали ошибку подписи")
		}
	})

	t.Run("expired", func(t *testing.T) {
		old := time.Now().Add(-48 * time.Hour).Unix()
		if _, err := validateInitData(sign(t, validFields(old)), testToken, 24*time.Hour); err == nil {
			t.Fatal("ожидали ошибку протухания")
		}
	})

	t.Run("no hash", func(t *testing.T) {
		if _, err := validateInitData("auth_date=1&user=%7B%7D", testToken, 24*time.Hour); err == nil {
			t.Fatal("ожидали ошибку")
		}
	})
}
