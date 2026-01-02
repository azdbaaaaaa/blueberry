package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"blueberry/internal/config"
	"blueberry/pkg/logger"

	"github.com/rs/zerolog"
	"github.com/spf13/cobra"
)

var (
	pushVideosChannelDir string
	pushVideosIndexStart int
	pushVideosIndexEnd   int
	pushVideosRemoteHost string
	pushVideosRemoteDir  string
	pushVideosRemoteUser string
)

var pushVideosCmd = &cobra.Command{
	Use:   "push-videos",
	Short: "根据 playlist_index 范围推送视频文件夹到服务器",
	Long: `根据本地 channel_info.json 中的 playlist_index 范围，选择视频文件夹并推送到远程服务器。
	
示例:
  # 推送 playlist_index 1-10 的视频到服务器
  blueberry push-videos --channel-dir downloads/Comic-likerhythm --index-start 1 --index-end 10 --remote-host 192.168.1.100 --remote-dir /opt/blueberry/downloads --remote-user root
  
  # 推送单个索引的视频
  blueberry push-videos --channel-dir downloads/Comic-likerhythm --index-start 5 --index-end 5 --remote-host 192.168.1.100
`,
	Run: func(cmd *cobra.Command, args []string) {
		cfg := config.Get()
		if cfg == nil {
			fmt.Fprintf(os.Stderr, "配置未加载\n")
			os.Exit(1)
		}

		if pushVideosChannelDir == "" {
			fmt.Fprintf(os.Stderr, "请指定频道目录（--channel-dir）\n")
			os.Exit(1)
		}

		if pushVideosIndexStart <= 0 || pushVideosIndexEnd <= 0 {
			fmt.Fprintf(os.Stderr, "请指定有效的索引范围（--index-start 和 --index-end 必须大于 0）\n")
			os.Exit(1)
		}

		if pushVideosIndexStart > pushVideosIndexEnd {
			fmt.Fprintf(os.Stderr, "索引范围无效：--index-start (%d) 不能大于 --index-end (%d)\n", pushVideosIndexStart, pushVideosIndexEnd)
			os.Exit(1)
		}

		if pushVideosRemoteHost == "" {
			fmt.Fprintf(os.Stderr, "请指定远程服务器地址（--remote-host）\n")
			os.Exit(1)
		}

		logger.SetLevel(zerolog.InfoLevel)

		// 解析频道目录路径
		absChannelDir, err := filepath.Abs(pushVideosChannelDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "解析频道目录路径失败: %v\n", err)
			os.Exit(1)
		}

		// 读取 channel_info.json
		channelInfoPath := filepath.Join(absChannelDir, "channel_info.json")
		if _, err := os.Stat(channelInfoPath); os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "未找到 channel_info.json: %s\n", channelInfoPath)
			os.Exit(1)
		}

		data, err := os.ReadFile(channelInfoPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "读取 channel_info.json 失败: %v\n", err)
			os.Exit(1)
		}

		var videos []map[string]interface{}
		if err := json.Unmarshal(data, &videos); err != nil {
			fmt.Fprintf(os.Stderr, "解析 channel_info.json 失败: %v\n", err)
			os.Exit(1)
		}

		logger.Info().
			Str("channel_dir", absChannelDir).
			Int("total_videos", len(videos)).
			Int("index_start", pushVideosIndexStart).
			Int("index_end", pushVideosIndexEnd).
			Msg("开始筛选视频")

		// 根据 playlist_index 筛选视频
		var selectedVideos []map[string]interface{}
		for _, video := range videos {
			playlistIndex, ok := video["playlist_index"]
			if !ok {
				continue
			}

			var index int
			switch v := playlistIndex.(type) {
			case float64:
				index = int(v)
			case int:
				index = v
			default:
				continue
			}

			if index >= pushVideosIndexStart && index <= pushVideosIndexEnd {
				selectedVideos = append(selectedVideos, video)
			}
		}

		if len(selectedVideos) == 0 {
			fmt.Fprintf(os.Stderr, "未找到 playlist_index 在 [%d, %d] 范围内的视频\n", pushVideosIndexStart, pushVideosIndexEnd)
			os.Exit(1)
		}

		logger.Info().
			Int("selected_count", len(selectedVideos)).
			Msg("筛选完成，开始推送视频文件夹")

		// 设置默认值
		if pushVideosRemoteDir == "" {
			pushVideosRemoteDir = "/opt/blueberry/downloads"
		}
		if pushVideosRemoteUser == "" {
			pushVideosRemoteUser = "root"
		}

		// 提取频道ID（从目录名）
		channelID := filepath.Base(absChannelDir)
		remoteChannelDir := filepath.Join(pushVideosRemoteDir, channelID)

		// 在远程服务器上创建频道目录
		logger.Info().
			Str("remote_host", pushVideosRemoteHost).
			Str("remote_dir", remoteChannelDir).
			Msg("在远程服务器上创建频道目录")

		sshCmd := exec.Command("ssh", "-o", "StrictHostKeyChecking=no",
			fmt.Sprintf("%s@%s", pushVideosRemoteUser, pushVideosRemoteHost),
			fmt.Sprintf("mkdir -p %s", remoteChannelDir))
		if err := sshCmd.Run(); err != nil {
			logger.Warn().
				Err(err).
				Str("remote_dir", remoteChannelDir).
				Msg("创建远程频道目录失败（可能已存在），继续推送")
		}

		// 推送每个视频文件夹
		successCount := 0
		failCount := 0

		for _, video := range selectedVideos {
			videoID, _ := video["id"].(string)
			if videoID == "" {
				logger.Warn().Msg("视频 ID 为空，跳过")
				failCount++
				continue
			}

			playlistIndex, _ := video["playlist_index"].(float64)
			title, _ := video["title"].(string)

			localVideoDir := filepath.Join(absChannelDir, videoID)
			remoteVideoDir := filepath.Join(remoteChannelDir, videoID)

			// 检查本地视频文件夹是否存在
			if _, err := os.Stat(localVideoDir); os.IsNotExist(err) {
				logger.Warn().
					Str("video_id", videoID).
					Int("playlist_index", int(playlistIndex)).
					Str("local_dir", localVideoDir).
					Msg("本地视频文件夹不存在，跳过")
				failCount++
				continue
			}

			logger.Info().
				Str("video_id", videoID).
				Int("playlist_index", int(playlistIndex)).
				Str("title", title).
				Str("local_dir", localVideoDir).
				Str("remote_dir", remoteVideoDir).
				Msg("推送视频文件夹")

			// 使用 rsync 推送文件夹
			// 确保本地目录路径以 / 结尾（rsync 要求）
			localPath := localVideoDir
			if !strings.HasSuffix(localPath, "/") {
				localPath = localPath + "/"
			}

			// 远程路径格式：user@host:path
			remotePath := fmt.Sprintf("%s@%s:%s/", pushVideosRemoteUser, pushVideosRemoteHost, remoteVideoDir)

			// 构建 rsync 命令
			rsyncArgs := []string{
				"-azP",
				"--partial",
				"--inplace",
				"-e", "ssh -o StrictHostKeyChecking=no",
				localPath,
				remotePath,
			}

			cmd := exec.Command("rsync", rsyncArgs...)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr

			cmdStr := "rsync " + strings.Join(rsyncArgs, " ")
			logger.Debug().Str("command", cmdStr).Msg("执行 rsync 命令")

			if err := cmd.Run(); err != nil {
				logger.Error().
					Err(err).
					Str("video_id", videoID).
					Str("command", cmdStr).
					Msg("推送视频文件夹失败")
				failCount++
				continue
			}

			logger.Info().
				Str("video_id", videoID).
				Int("playlist_index", int(playlistIndex)).
				Msg("视频文件夹推送成功")
			successCount++
		}

		logger.Info().
			Int("total", len(selectedVideos)).
			Int("success", successCount).
			Int("failed", failCount).
			Msg("推送完成")

		if failCount > 0 {
			fmt.Fprintf(os.Stderr, "部分视频推送失败（%d/%d）\n", failCount, len(selectedVideos))
			os.Exit(1)
		}
	},
}

func init() {
	pushVideosCmd.Flags().StringVar(&pushVideosChannelDir, "channel-dir", "", "频道目录路径（例如：downloads/Comic-likerhythm）")
	pushVideosCmd.Flags().IntVar(&pushVideosIndexStart, "index-start", 0, "起始 playlist_index（包含）")
	pushVideosCmd.Flags().IntVar(&pushVideosIndexEnd, "index-end", 0, "结束 playlist_index（包含）")
	pushVideosCmd.Flags().StringVar(&pushVideosRemoteHost, "remote-host", "", "远程服务器地址（例如：192.168.1.100）")
	pushVideosCmd.Flags().StringVar(&pushVideosRemoteDir, "remote-dir", "/opt/blueberry/downloads", "远程服务器目录（默认：/opt/blueberry/downloads）")
	pushVideosCmd.Flags().StringVar(&pushVideosRemoteUser, "remote-user", "root", "远程服务器用户（默认：root）")
	rootCmd.AddCommand(pushVideosCmd)
}

