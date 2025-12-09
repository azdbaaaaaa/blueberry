package cmd

import (
	"context"
	"fmt"
	"os"

	"blueberry/internal/app"
	"blueberry/internal/config"
	"blueberry/pkg/logger"

	"github.com/rs/zerolog"
	"github.com/spf13/cobra"
)

var (
	uploadVideoPath string
	uploadAccount   string
)

var uploadCmd = &cobra.Command{
	Use:   "upload",
	Short: "上传视频到B站",
	Long:  `将下载的视频上传到B站海外版指定的账号`,
	Run: func(cmd *cobra.Command, args []string) {
		cfg := config.Get()
		if cfg == nil {
			fmt.Fprintf(os.Stderr, "配置未加载\n")
			os.Exit(1)
		}

		application, err := app.NewApp(cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "初始化应用失败: %v\n", err)
			os.Exit(1)
		}

		logger.SetLevel(zerolog.InfoLevel)

		if uploadVideoPath == "" {
			fmt.Fprintf(os.Stderr, "请指定要上传的视频路径\n")
			os.Exit(1)
		}

		accountName := uploadAccount
		if accountName == "" {
			fmt.Fprintf(os.Stderr, "请指定B站账号名称\n")
			os.Exit(1)
		}

		if _, exists := cfg.BilibiliAccounts[accountName]; !exists {
			fmt.Fprintf(os.Stderr, "账号 %s 不存在\n", accountName)
			os.Exit(1)
		}

		ctx := context.Background()
		if err := application.UploadService.UploadSingleVideo(ctx, uploadVideoPath, accountName); err != nil {
			os.Exit(1)
		}
	},
}

func init() {
	uploadCmd.Flags().StringVar(&uploadVideoPath, "video", "", "要上传的视频文件路径")
	uploadCmd.Flags().StringVar(&uploadAccount, "account", "", "B站账号名称")
}
