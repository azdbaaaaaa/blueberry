package cmd

import (
	"fmt"
	"os"

	"blueberry/internal/app"
	"blueberry/internal/config"
	"blueberry/pkg/logger"

	"github.com/rs/zerolog"
	"github.com/spf13/cobra"
)

var (
	renameAID       string
	renameSubtitles []string
)

var renameCmd = &cobra.Command{
	Use:   "rename",
	Short: "重命名字幕文件",
	Long:  `根据B站视频aid重命名字幕文件，格式为 {aid}_{lang}.srt`,
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

		if renameAID == "" {
			fmt.Fprintf(os.Stderr, "请指定aid\n")
			os.Exit(1)
		}

		if len(renameSubtitles) == 0 {
			fmt.Fprintf(os.Stderr, "请指定字幕文件路径\n")
			os.Exit(1)
		}

		logger.SetLevel(zerolog.InfoLevel)

		renamedPaths, err := application.UploadService.RenameSubtitlesForAID(renameSubtitles, renameAID)
		if err != nil {
			logger.Error().Err(err).Msg("重命名失败")
			os.Exit(1)
		}

		logger.Info().Int("count", len(renamedPaths)).Msg("重命名完成，共处理字幕文件")
		for _, path := range renamedPaths {
			logger.Info().Str("path", path).Msg("重命名文件")
		}
	},
}

func init() {
	renameCmd.Flags().StringVar(&renameAID, "aid", "", "B站视频aid（必需）")
	renameCmd.Flags().StringSliceVar(&renameSubtitles, "subtitles", []string{}, "字幕文件路径列表（必需）")
	rootCmd.AddCommand(renameCmd)
}
