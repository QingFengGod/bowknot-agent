// Bowknot Agent 是本地优先的桌面 AI 工作台。
//
// 根目录 main.go 只做装配：把进程交给 internal/app，业务逻辑不写在这里。
package main

import (
	"log"

	"github.com/QingFengGod/bowknot-agent/internal/app"
)

func main() {
	if err := app.New().Run(); err != nil {
		log.Fatal(err)
	}
}
