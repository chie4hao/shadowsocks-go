package shadowsocks

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/des"
	"crypto/md5"
	"crypto/rand"
	"crypto/rc4"
	"errors"
	"io"
	// "github.com/aead/chacha20"
	// "golang.org/x/crypto/blowfish"
	// "golang.org/x/crypto/cast5"
	// "golang.org/x/crypto/salsa20/salsa"
)

var errEmptyPassword = errors.New("empty key")

func md5sum(d []byte) []byte {
	h := md5.New()
	h.Write(d)
	return h.Sum(nil)
}

func evpBytesToKey(password string, keyLen int) (key []byte) {
	const md5Len = 16

	cnt := (keyLen-1)/md5Len + 1
	m := make([]byte, cnt*md5Len)
	copy(m, md5sum([]byte(password)))

	// Repeatedly call md5 until bytes generated is enough.
	// Each call to md5 uses data: prev md5 sum + password.
	d := make([]byte, md5Len+len(password))
	start := 0
	for i := 1; i < cnt; i++ {
		start += md5Len
		copy(d, m[start-md5Len:start])
		copy(d[md5Len:], password)
		copy(m[start:], md5sum(d))
	}
	return m[:keyLen]
}

type DecOrEnc int

const (
	Decrypt DecOrEnc = iota
	Encrypt
)

func newStream(block cipher.Block, err error, key, iv []byte,
	doe DecOrEnc) (cipher.Stream, error) {
	if err != nil {
		return nil, err
	}
	if doe == Encrypt {
		return cipher.NewCFBEncrypter(block, iv), nil
	} else {
		return cipher.NewCFBDecrypter(block, iv), nil
	}
}

func newAESCFBStream(key, iv []byte, doe DecOrEnc) (cipher.Stream, error) {
	block, err := aes.NewCipher(key)
	return newStream(block, err, key, iv, doe)
}

func newAESCTRStream(key, iv []byte, doe DecOrEnc) (cipher.Stream, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewCTR(block, iv), nil
}

func newDESStream(key, iv []byte, doe DecOrEnc) (cipher.Stream, error) {
	block, err := des.NewCipher(key)
	return newStream(block, err, key, iv, doe)
}

func newRC4MD5Stream(key, iv []byte, _ DecOrEnc) (cipher.Stream, error) {
	h := md5.New()
	h.Write(key)
	h.Write(iv)
	rc4key := h.Sum(nil)

	return rc4.NewCipher(rc4key)
}

type cipherInfo struct {
	keyLen    int
	ivLen     int
	newStream func(key, iv []byte, doe DecOrEnc) (cipher.Stream, error)
}

var cipherMethod = map[string]*cipherInfo{
	"aes-128-cfb": {16, 16, newAESCFBStream},
	"aes-192-cfb": {24, 16, newAESCFBStream},
	"aes-256-cfb": {32, 16, newAESCFBStream},
	"aes-128-ctr": {16, 16, newAESCTRStream},
	"aes-192-ctr": {24, 16, newAESCTRStream},
	"aes-256-ctr": {32, 16, newAESCTRStream},
	"des-cfb":     {8, 8, newDESStream},
	"rc4-md5":     {16, 16, newRC4MD5Stream},
	"rc4-md5-6":   {16, 6, newRC4MD5Stream},
}

func CheckCipherMethod(method string) error {
	if method == "" {
		method = "aes-256-cfb"
	}
	_, ok := cipherMethod[method]
	if !ok {
		return errors.New("Unsupported encryption method: " + method)
	}
	return nil
}

type Cipher struct {
	enc  cipher.Stream
	dec  cipher.Stream
	key  []byte
	info *cipherInfo
	iv   []byte
}

// NewCipher creates a cipher that can be used in Dial() etc.
// Use cipher.Copy() to create a new cipher with the same method and password
// to avoid the cost of repeated cipher initialization.
func NewCipher(method, password string) (c *Cipher, err error) {
	if password == "" {
		return nil, errEmptyPassword
	}
	mi, ok := cipherMethod[method]
	if !ok {
		return nil, errors.New("Unsupported encryption method: " + method)
	}

	key := evpBytesToKey(password, mi.keyLen)

	c = &Cipher{key: key, info: mi}

	if err != nil {
		return nil, err
	}
	return c, nil
}

// Initializes the block cipher with CFB mode, returns IV.
func (c *Cipher) initEncrypt() (iv []byte, err error) {
	if c.iv == nil {
		iv = make([]byte, c.info.ivLen)
		if _, err := io.ReadFull(rand.Reader, iv); err != nil {
			return nil, err
		}
		c.iv = iv
	} else {
		iv = c.iv
	}
	c.enc, err = c.info.newStream(c.key, iv, Encrypt)
	return
}

func (c *Cipher) initDecrypt(iv []byte) (err error) {
	c.dec, err = c.info.newStream(c.key, iv, Decrypt)
	return
}

func (c *Cipher) encrypt(dst, src []byte) {
	c.enc.XORKeyStream(dst, src)
}

func (c *Cipher) decrypt(dst, src []byte) {
	c.dec.XORKeyStream(dst, src)
}

// Copy creates a new cipher at it's initial state.
func (c *Cipher) Copy() *Cipher {
	// This optimization maybe not necessary. But without this function, we
	// need to maintain a table cache for newTableCipher and use lock to
	// protect concurrent access to that cache.

	// AES and DES ciphers does not return specific types, so it's difficult
	// to create copy. But their initizliation time is less than 4000ns on my
	// 2.26 GHz Intel Core 2 Duo processor. So no need to worry.

	// Currently, blow-fish and cast5 initialization cost is an order of
	// maganitude slower than other ciphers. (I'm not sure whether this is
	// because the current implementation is not highly optimized, or this is
	// the nature of the algorithm.)

	nc := *c
	nc.enc = nil
	nc.dec = nil
	return &nc
}
