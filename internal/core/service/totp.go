package service

import (
	"crypto/hmac"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"time"
)

// TOTP per RFC 6238 with the parameters every authenticator app supports: SHA-1, 30-second steps, 6 digits
const (
	totpPeriod     = 30
	totpDigits     = 6
	totpSecretSize = 20
	// One step either side tolerates a phone clock that is up to 30 s off
	totpSkew = 1
)

var totpEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

func totpStep(t time.Time) int64 { return t.Unix() / totpPeriod }

func totpCode(secret []byte, step int64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step))
	mac := hmac.New(sha1.New, secret)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	value := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", value%1_000_000)
}

// totpMatch returns the step the code belongs to, within the allowed skew
func totpMatch(secret []byte, code string, now time.Time) (int64, bool) {
	if len(code) != totpDigits {
		return 0, false
	}
	current := totpStep(now)
	for delta := int64(-totpSkew); delta <= totpSkew; delta++ {
		step := current + delta
		if subtle.ConstantTimeCompare([]byte(totpCode(secret, step)), []byte(code)) == 1 {
			return step, true
		}
	}
	return 0, false
}

// totpURI is what the QR code encodes (otpauth://totp/Issuer:account?...)
func totpURI(issuer, account string, secret []byte) string {
	label := url.PathEscape(issuer + ":" + account)
	q := url.Values{
		"secret":    {totpEncoding.EncodeToString(secret)},
		"issuer":    {issuer},
		"algorithm": {"SHA1"},
		"digits":    {fmt.Sprint(totpDigits)},
		"period":    {fmt.Sprint(totpPeriod)},
	}
	return "otpauth://totp/" + label + "?" + q.Encode()
}
