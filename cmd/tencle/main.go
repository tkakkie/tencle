// Command tencle は tencle の唯一のバイナリである。サブコマンドで役割（serve・setup・create-tenant など）を切り替える。
package main

import (
	"fmt"
	"io"
	"os"
)

const usage = `使い方: tencle <サブコマンド>

サブコマンド:
  （まだありません）
`

// 引数の誤りは、シェルの慣習（組み込みコマンドの誤用）に合わせて 2 で終える。
const exitUsage = 2

func main() {
	os.Exit(run(os.Args[1:], os.Stderr))
}

// run は、テストから終了コードと出力を確かめられるように main から分けている。
// 標準エラーへの書き込みの失敗は、知らせる先がないので捨てる。
func run(args []string, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprint(stderr, usage)
		return exitUsage
	}
	_, _ = fmt.Fprintf(stderr, "不明なサブコマンドです: %q\n\n%s", args[0], usage)
	return exitUsage
}
