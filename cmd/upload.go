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
	uploadChannel   string
	uploadAll       bool
)

var uploadCmd = &cobra.Command{
	Use:   "upload",
	Short: "上传视频到B站",
	Long:  `将下载的视频上传到B站海外版指定的账号。可以上传单个视频或整个频道。`,
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

		ctx := context.Background()

		// 如果指定了 --all，上传所有频道
		if uploadAll {
			logger.Info().Msg("开始上传所有频道")
			if err := application.UploadService.UploadAllChannels(ctx); err != nil {
				logger.Error().Err(err).Msg("上传所有频道失败")
				os.Exit(1)
			}
			return
		}

		// 如果指定了 --channel，上传指定频道
		if uploadChannel != "" {
			logger.Info().Str("channel", uploadChannel).Msg("开始上传频道")
			if err := application.UploadService.UploadChannel(ctx, uploadChannel); err != nil {
				logger.Error().Err(err).Str("channel", uploadChannel).Msg("上传频道失败")
				os.Exit(1)
			}
			return
		}

		// 否则，上传单个视频
		if uploadVideoPath == "" {
			fmt.Fprintf(os.Stderr, "请指定要上传的视频路径（--video），或指定频道（--channel），或上传所有频道（--all）\n")
			os.Exit(1)
		}

		accountName := uploadAccount
		if accountName == "" {
			fmt.Fprintf(os.Stderr, "请指定B站账号名称（--account）\n")
			os.Exit(1)
		}

		if _, exists := cfg.BilibiliAccounts[accountName]; !exists {
			fmt.Fprintf(os.Stderr, "账号 %s 不存在\n", accountName)
			os.Exit(1)
		}

		if err := application.UploadService.UploadSingleVideo(ctx, uploadVideoPath, accountName); err != nil {
			os.Exit(1)
		}
	},
}

func init() {
	uploadCmd.Flags().StringVar(&uploadVideoPath, "video", "", "要上传的视频文件路径（单个视频模式）")
	uploadCmd.Flags().StringVar(&uploadAccount, "account", "", "B站账号名称（单个视频模式）")
	uploadCmd.Flags().StringVar(&uploadChannel, "channel", "", "要上传的频道URL（频道模式）")
	uploadCmd.Flags().BoolVar(&uploadAll, "all", false, "上传配置文件中所有频道（全部频道模式）")
}
