package admin

import (
	"bufio"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/crypto/argon2"
	"golang.org/x/term"
)

// Параметры argon2id: ~64 МиБ памяти на попытку (достаточно для небольшого VPS при ограничении попыток входа).
const (
	argonTime    = 3
	argonMemory  = 64 * 1024
	argonThreads = 2
	argonKeyLen  = 32
	saltLen      = 16
	MinPassword  = 12
)

var b64 = base64.RawStdEncoding

// HashPassword возвращает хэш в формате PHC: $argon2id$v=19$m=...,t=...,p=...$соль$хэш.
func HashPassword(password string) (string, error) {
	if len(password) < MinPassword {
		return "", fmt.Errorf("пароль короче %d символов", MinPassword)
	}
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, argonMemory, argonTime, argonThreads,
		b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

type parsedHash struct {
	salt, key []byte
	m, t, p   int
}

// parseHash разбирает PHC-строку и проверяет границы параметров.
func parseHash(encoded string) (parsedHash, bool) {
	var h parsedHash
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return h, false
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return h, false
	}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &h.m, &h.t, &h.p); err != nil {
		return h, false
	}
	// Границы защищают от случайно испорченной настройки (память/время).
	if h.m < 8*1024 || h.m > 512*1024 || h.t < 1 || h.t > 10 || h.p < 1 || h.p > 8 {
		return h, false
	}
	var err error
	if h.salt, err = b64.DecodeString(parts[4]); err != nil || len(h.salt) < 8 {
		return h, false
	}
	if h.key, err = b64.DecodeString(parts[5]); err != nil || len(h.key) < 16 {
		return h, false
	}
	return h, true
}

// ValidHash сообщает, что строка — корректный хэш argon2id (иначе вход был бы невозможен).
func ValidHash(encoded string) bool { _, ok := parseHash(encoded); return ok }

// NormalizeHash убирает пробелы и парные кавычки по краям (значение могли вставить вместе с кавычками).
func NormalizeHash(v string) string {
	v = strings.TrimSpace(v)
	if len(v) >= 2 && (v[0] == '\'' || v[0] == '"') && v[len(v)-1] == v[0] {
		v = strings.TrimSpace(v[1 : len(v)-1])
	}
	return v
}

// VerifyPassword проверяет пароль за постоянное время; на любую ошибку разбора отвечает false.
func VerifyPassword(encoded, password string) bool {
	h, ok := parseHash(encoded)
	if !ok {
		return false
	}
	got := argon2.IDKey([]byte(password), h.salt, uint32(h.t), uint32(h.m), uint8(h.p), uint32(len(h.key)))
	return subtle.ConstantTimeCompare(got, h.key) == 1
}

// HashPasswordCLI — команда «lunexgame hash-password»: читает пароль (скрытый ввод в терминале, иначе из stdin)
// и печатает строку для файла окружения. Пароль не попадает в историю команд и журналы.
func HashPasswordCLI(in *os.File, out, errOut io.Writer) error {
	read := func(prompt string) (string, error) {
		fmt.Fprint(errOut, prompt)
		if term.IsTerminal(int(in.Fd())) {
			b, err := term.ReadPassword(int(in.Fd()))
			fmt.Fprintln(errOut)
			return string(b), err
		}
		line, err := bufio.NewReader(in).ReadString('\n')
		if err != nil && line == "" {
			return "", err
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	pw, err := read("Пароль администратора (не короче 12 символов): ")
	if err != nil {
		return err
	}
	if term.IsTerminal(int(in.Fd())) {
		again, err := read("Повторите пароль: ")
		if err != nil {
			return err
		}
		if again != pw {
			return errors.New("пароли не совпали")
		}
	}
	h, err := HashPassword(pw)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "LUNEXGAME_ADMIN_PASSWORD_HASH='%s'\n", h)
	return nil
}
