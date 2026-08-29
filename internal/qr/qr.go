package qr

import (
	"fmt"

	"github.com/skip2/go-qrcode"
)

func GenerateTerminal(content string) error {
	qr, err := qrcode.New(content, qrcode.Medium)
	if err != nil {
		return err
	}
	fmt.Println()
	fmt.Println(qr.ToSmallString(false))
	return nil
}
