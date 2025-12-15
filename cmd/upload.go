package cmd

import (
	"context"
	"fmt"
	"os"
	"time"

	"blueberry/internal/app"
	"blueberry/internal/config"
	"blueberry/pkg/logger"

	"github.com/rs/zerolog"
	"github.com/spf13/cobra"
)

var (
	uploadVideoPath  string
	uploadAccount    string
	uploadChannel    string
	uploadChannelDir string
	uploadAll        bool
	uploadWatch      bool
	uploadIntervalM  int
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
			for {
				logger.Info().Msg("开始上传所有频道")
				if err := application.UploadService.UploadAllChannels(ctx); err != nil {
					logger.Error().Err(err).Msg("上传所有频道失败")
					if !uploadWatch {
						os.Exit(1)
					}
				}
				if !uploadWatch {
					return
				}
				interval := time.Duration(uploadIntervalM) * time.Minute
				logger.Info().Dur("sleep", interval).Msg("本轮上传完成，进入休眠等待下一轮")
				time.Sleep(interval)
			}
		}

		// 如果指定了 --channel，上传指定频道（URL）
		if uploadChannel != "" {
			for {
				logger.Info().Str("channel", uploadChannel).Msg("开始上传频道")
				if err := application.UploadService.UploadChannel(ctx, uploadChannel); err != nil {
					logger.Error().Err(err).Str("channel", uploadChannel).Msg("上传频道失败")
					if !uploadWatch {
						os.Exit(1)
					}
				}
				if !uploadWatch {
					return
				}
				interval := time.Duration(uploadIntervalM) * time.Minute
				logger.Info().Dur("sleep", interval).Msg("本轮上传完成，进入休眠等待下一轮")
				time.Sleep(interval)
			}
		}

		// 如果指定了 --channel-dir，上传本地频道目录
		if uploadChannelDir != "" {
			for {
				logger.Info().Str("channel_dir", uploadChannelDir).Msg("开始上传频道目录")
				if err := application.UploadService.UploadChannelDir(ctx, uploadChannelDir); err != nil {
					logger.Error().Err(err).Str("channel_dir", uploadChannelDir).Msg("上传频道目录失败")
					if !uploadWatch {
						os.Exit(1)
					}
				}
				if !uploadWatch {
					return
				}
				interval := time.Duration(uploadIntervalM) * time.Minute
				logger.Info().Dur("sleep", interval).Msg("本轮上传完成，进入休眠等待下一轮")
				time.Sleep(interval)
			}
		}

		// 否则，上传单个视频
		if uploadVideoPath == "" {
			fmt.Fprintf(os.Stderr, "请指定：--video-dir（单视频）或 --channel（频道URL）或 --channel-dir（频道目录）或 --all（全部频道）\n")
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

		for {
			if err := application.UploadService.UploadSingleVideo(ctx, uploadVideoPath, accountName); err != nil {
				if !uploadWatch {
					os.Exit(1)
				}
				logger.Error().Err(err).Str("video_dir", uploadVideoPath).Msg("单视频上传失败")
			}
			if !uploadWatch {
				return
			}
			interval := time.Duration(uploadIntervalM) * time.Minute
			logger.Info().Dur("sleep", interval).Msg("本轮上传完成，进入休眠等待下一轮")
			time.Sleep(interval)
		}
	},
}

func init() {
	uploadCmd.Flags().StringVar(&uploadVideoPath, "video-dir", "", "要上传的视频目录路径（单个视频模式）")
	uploadCmd.Flags().StringVar(&uploadAccount, "account", "", "B站账号名称（单个视频模式）")
	uploadCmd.Flags().StringVar(&uploadChannel, "channel", "", "要上传的频道URL（频道模式）")
	uploadCmd.Flags().StringVar(&uploadChannelDir, "channel-dir", "", "要上传的本地频道目录（频道模式）")
	uploadCmd.Flags().BoolVar(&uploadAll, "all", false, "上传配置文件中所有频道（全部频道模式）")
	uploadCmd.Flags().BoolVar(&uploadWatch, "watch", false, "持续循环上传；每轮结束后休眠并再次扫描上传")
	uploadCmd.Flags().IntVar(&uploadIntervalM, "interval-minutes", 5, "watch 模式的每轮间隔（分钟）")
}
