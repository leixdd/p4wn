package pdf

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/rc4"
	"crypto/sha256"
	"crypto/sha512"
	"errors"
	"fmt"
)

func sha384(b []byte) []byte { s := sha512.Sum384(b); return s[:] }

func sha512Sum(b []byte) []byte { s := sha512.Sum512(b); return s[:] }

// decryptObject walks a freshly parsed indirect object, decrypting strings
// and tagging streams with their object number for lazy data decryption.
func (d *Document) decryptObject(obj Object, num, gen int) Object {
	switch v := obj.(type) {
	case String:
		return String(d.decrypt([]byte(v), num, gen, d.crypt.strMethod))
	case Array:
		for i, o := range v {
			v[i] = d.decryptObject(o, num, gen)
		}
		return v
	case Dict:
		for k, o := range v {
			v[k] = d.decryptObject(o, num, gen)
		}
		return v
	case *Stream:
		v.Dict = d.decryptObject(v.Dict, num, gen).(Dict)
		v.cryptNum, v.cryptGen = num, gen
		return v
	}
	return obj
}

// Standard security handler (/Filter /Standard), empty-user-password only.
// Revisions: 2/3/4 (RC4 or AES-128 with MD5 key derivation) and 5/6
// (AES-256 with SHA-256 family derivation).

var passwordPad = []byte{
	0x28, 0xBF, 0x4E, 0x5E, 0x4E, 0x75, 0x8A, 0x41, 0x64, 0x00, 0x4E, 0x56,
	0xFF, 0xFA, 0x01, 0x08, 0x2E, 0x2E, 0x00, 0xB6, 0xD0, 0x68, 0x3E, 0x80,
	0x2F, 0x0C, 0xA9, 0xFE, 0x64, 0x53, 0x69, 0x7A,
}

type cryptMethod int

const (
	cryptNone cryptMethod = iota
	cryptRC4
	cryptAESV2 // AES-128-CBC
	cryptAESV3 // AES-256-CBC
)

type crypt struct {
	key       []byte
	strMethod cryptMethod
	stmMethod cryptMethod
	r         int
}

// initCrypt authenticates the empty user password and derives the file key.
func (d *Document) initCrypt() error {
	encObj := d.trailer.Get("Encrypt")
	if encObj == nil || IsNull(encObj) {
		return nil
	}
	// resolve without decryption (the /Encrypt dict itself is never encrypted)
	enc := d.GetDict(encObj)
	if enc == nil {
		return errors.New("pdf: bad /Encrypt")
	}
	if f, _ := enc.GetName("Filter"); f != "Standard" {
		return fmt.Errorf("pdf: unsupported security handler %q", f)
	}
	v, _ := d.GetInt(enc.Get("V"))
	r, _ := d.GetInt(enc.Get("R"))
	length, _ := d.GetInt(enc.Get("Length"))
	if length == 0 {
		length = 40
	}
	oStr, _ := d.Resolve(enc.Get("O")).(String)
	uStr, _ := d.Resolve(enc.Get("U")).(String)
	pInt, _ := d.GetInt(enc.Get("P"))

	var firstID []byte
	if ids := d.GetArray(d.trailer.Get("ID")); len(ids) > 0 {
		if s, ok := d.Resolve(ids[0]).(String); ok {
			firstID = []byte(s)
		}
	}

	c := &crypt{r: int(r), strMethod: cryptRC4, stmMethod: cryptRC4}

	// V4/V5 crypt filters: look up /StmF & /StrF methods in /CF
	if v >= 4 {
		cfDict := d.GetDict(enc.Get("CF"))
		method := func(name Name) cryptMethod {
			if name == "" || name == "Identity" {
				return cryptNone
			}
			f := d.GetDict(cfDict.Get(name))
			if f == nil {
				return cryptNone
			}
			cfm, _ := f.GetName("CFM")
			switch cfm {
			case "V2":
				return cryptRC4
			case "AESV2":
				return cryptAESV2
			case "AESV3":
				return cryptAESV3
			}
			return cryptNone
		}
		stmF, _ := enc.GetName("StmF")
		strF, _ := enc.GetName("StrF")
		c.stmMethod = method(stmF)
		c.strMethod = method(strF)
	}

	switch {
	case r >= 2 && r <= 4:
		keyLen := int(length) / 8
		if keyLen < 5 {
			keyLen = 5
		}
		if keyLen > 16 {
			keyLen = 16
		}
		key := computeKeyR234(passwordPad, oStr, uint32(pInt), firstID, keyLen, int(r), metadataEncrypted(d, enc))
		// verify against /U
		if !verifyUserR234(key, uStr, firstID, int(r)) {
			return errors.New("pdf: password required")
		}
		c.key = key
	case r == 5 || r == 6:
		key, err := computeKeyR56(nil, oStr, uStr, d, enc)
		if err != nil {
			return err
		}
		c.key = key
		if c.stmMethod == cryptRC4 {
			c.stmMethod = cryptAESV3
		}
		if c.strMethod == cryptRC4 {
			c.strMethod = cryptAESV3
		}
	default:
		return fmt.Errorf("pdf: unsupported encryption revision %d", r)
	}
	d.crypt = c
	return nil
}

func metadataEncrypted(d *Document, enc Dict) bool {
	if b, ok := d.Resolve(enc.Get("EncryptMetadata")).(Bool); ok {
		return bool(b)
	}
	return true
}

// computeKeyR234 derives the file key: MD5(paddedPwd ‖ O ‖ P-LE ‖ ID[0]
// [‖ FFFFFFFF if !encryptMetadata]), then 50 MD5 rounds for R≥3.
func computeKeyR234(paddedPwd []byte, o String, p uint32, id []byte, keyLen, r int, encMeta bool) []byte {
	h := md5.New()
	h.Write(paddedPwd)
	ob := make([]byte, 32)
	copy(ob, o)
	h.Write(ob)
	h.Write([]byte{byte(p), byte(p >> 8), byte(p >> 16), byte(p >> 24)})
	h.Write(id)
	if r >= 4 && !encMeta {
		h.Write([]byte{0xFF, 0xFF, 0xFF, 0xFF})
	}
	key := h.Sum(nil)
	if r >= 3 {
		for i := 0; i < 50; i++ {
			s := md5.Sum(key[:keyLen])
			key = s[:]
		}
	}
	return key[:keyLen]
}

// verifyUserR234 recomputes /U from the key and compares.
func verifyUserR234(key []byte, u String, id []byte, r int) bool {
	if len(u) < 16 {
		return false
	}
	if r == 2 {
		c, _ := rc4.NewCipher(key)
		out := make([]byte, 32)
		c.XORKeyStream(out, passwordPad)
		return bytes.Equal(out[:16], []byte(u)[:16])
	}
	// r >= 3: MD5(pad ‖ ID), RC4 with key, then 19 rounds with mutated keys
	h := md5.New()
	h.Write(passwordPad)
	h.Write(id)
	sum := h.Sum(nil)
	c, _ := rc4.NewCipher(key)
	out := make([]byte, 16)
	c.XORKeyStream(out, sum)
	tmpKey := make([]byte, len(key))
	for i := 1; i <= 19; i++ {
		for j := range key {
			tmpKey[j] = key[j] ^ byte(i)
		}
		c, _ := rc4.NewCipher(tmpKey)
		c.XORKeyStream(out, out)
	}
	return bytes.Equal(out, []byte(u)[:16])
}

// computeKeyR56 authenticates the empty user password for AES-256
// (R5: simple SHA-256; R6: the iterated hash) and unwraps the file key.
func computeKeyR56(pwd []byte, o, u String, d *Document, enc Dict) ([]byte, error) {
	if len(u) < 48 {
		return nil, errors.New("pdf: short /U")
	}
	ub := []byte(u)
	validationSalt := ub[32:40]
	keySalt := ub[40:48]

	hash := hash56(pwd, validationSalt, nil, d.crypt56Rev(enc))
	if !bytes.Equal(hash, ub[:32]) {
		return nil, errors.New("pdf: password required")
	}
	interKey := hash56(pwd, keySalt, nil, d.crypt56Rev(enc))
	ueStr, _ := d.Resolve(enc.Get("UE")).(String)
	if len(ueStr) < 32 {
		return nil, errors.New("pdf: missing /UE")
	}
	block, err := aes.NewCipher(interKey)
	if err != nil {
		return nil, err
	}
	iv := make([]byte, 16)
	mode := cipher.NewCBCDecrypter(block, iv)
	fileKey := make([]byte, 32)
	mode.CryptBlocks(fileKey, []byte(ueStr)[:32])
	return fileKey, nil
}

func (d *Document) crypt56Rev(enc Dict) int {
	r, _ := d.GetInt(enc.Get("R"))
	return int(r)
}

// hash56 implements the R5 (single SHA-256) and R6 (Algorithm 2.B iterated)
// password hash.
func hash56(pwd, salt, udata []byte, r int) []byte {
	h := sha256.New()
	h.Write(pwd)
	h.Write(salt)
	h.Write(udata)
	k := h.Sum(nil)
	if r == 5 {
		return k
	}
	// R6 Algorithm 2.B
	var k1 []byte
	for round := 0; ; round++ {
		k1 = k1[:0]
		for i := 0; i < 64; i++ {
			k1 = append(k1, pwd...)
			k1 = append(k1, k...)
			k1 = append(k1, udata...)
		}
		block, _ := aes.NewCipher(k[:16])
		mode := cipher.NewCBCEncrypter(block, k[16:32])
		e := make([]byte, len(k1))
		mode.CryptBlocks(e, k1)
		sum := 0
		for _, b := range e[:16] {
			sum += int(b)
		}
		switch sum % 3 {
		case 0:
			s := sha256.Sum256(e)
			k = s[:]
		case 1:
			k = sha384(e)
		case 2:
			k = sha512Sum(e)
		}
		if round >= 63 && int(e[len(e)-1]) <= round-32 {
			return k[:32]
		}
	}
}

// decryptString/decryptStream apply per-object decryption in place-ish.
func (d *Document) decrypt(data []byte, num, gen int, method cryptMethod) []byte {
	if d.crypt == nil || method == cryptNone || len(data) == 0 {
		return data
	}
	key := d.crypt.key
	if d.crypt.r < 5 {
		// per-object key: MD5(fileKey ‖ num[3LE] ‖ gen[2LE] [‖ "sAlT" for AES])
		h := md5.New()
		h.Write(key)
		h.Write([]byte{byte(num), byte(num >> 8), byte(num >> 16), byte(gen), byte(gen >> 8)})
		if method == cryptAESV2 {
			h.Write([]byte{0x73, 0x41, 0x6C, 0x54})
		}
		sum := h.Sum(nil)
		n := len(key) + 5
		if n > 16 {
			n = 16
		}
		key = sum[:n]
	}
	switch method {
	case cryptRC4:
		c, err := rc4.NewCipher(key)
		if err != nil {
			return data
		}
		out := make([]byte, len(data))
		c.XORKeyStream(out, data)
		return out
	case cryptAESV2, cryptAESV3:
		if len(data) < 16 {
			return nil
		}
		block, err := aes.NewCipher(key)
		if err != nil {
			return data
		}
		iv := data[:16]
		body := data[16:]
		if len(body)%16 != 0 {
			body = body[:len(body)/16*16]
		}
		if len(body) == 0 {
			return nil
		}
		out := make([]byte, len(body))
		cipher.NewCBCDecrypter(block, iv).CryptBlocks(out, body)
		// strip PKCS#7 padding
		pad := int(out[len(out)-1])
		if pad >= 1 && pad <= 16 && pad <= len(out) {
			out = out[:len(out)-pad]
		}
		return out
	}
	return data
}
