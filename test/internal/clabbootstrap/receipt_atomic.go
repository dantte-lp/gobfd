package clabbootstrap

import (
	"errors"
	"os"

	"github.com/dantte-lp/gobfd/test/internal/interopproject"
)

func replaceLifecycleReceipt(path string, data []byte) error {
	return privateReceiptError(interopproject.ReplacePrivateReceipt(path, data))
}

func validateReceiptDirectory(directory *os.File, path string) error {
	return privateReceiptError(interopproject.ValidatePrivateReceiptDirectory(directory, path))
}

func inspectReceiptDestination(root *os.Root, name string) error {
	_, err := interopproject.InspectPrivateReceipt(root, name)
	return privateReceiptError(err)
}

func privateReceiptFile(info os.FileInfo) bool {
	return interopproject.IsPrivateReceipt(info)
}

func privateReceiptError(err error) error {
	if errors.Is(err, interopproject.ErrUnsafeReceipt) {
		return errors.Join(errLifecycleState, err)
	}
	return err
}
