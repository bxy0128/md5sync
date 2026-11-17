// 秒传 MD5 计算器：图形界面版。
//
// 界面资源通过 embed 打包进单个 exe，双击即可运行，不需要额外文件。
package main

import (
	"embed"
	"log"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:web
var assets embed.FS

func main() {
	app := NewApp()

	err := wails.Run(&options.App{
		Title:       "秒传 MD5 计算器",
		Width:       1020,
		Height:      760,
		MinWidth:    780,
		MinHeight:   600,
		AssetServer: &assetserver.Options{Assets: assets},
		BackgroundColour: &options.RGBA{
			R: 16, G: 18, B: 22, A: 1,
		},
		OnStartup: app.startup,
		Bind: []interface{}{
			app,
		},
		DragAndDrop: &options.DragAndDrop{
			EnableFileDrop: true,
		},
	})
	if err != nil {
		log.Fatal(err)
	}
}
