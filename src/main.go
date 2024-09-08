package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type NetStats struct {
	ReceiveBytes  uint64 `json:"receive_bytes"`
	TransmitBytes uint64 `json:"transmit_bytes"`
}

type Statistics struct {
	TotalReceive  uint64 `json:"total_receive"`
	TotalTransmit uint64 `json:"total_transmit"`
	LastReceive   uint64 `json:"last_receive"`
	LastTransmit  uint64 `json:"last_transmit"`
	LastReset     string `json:"last_reset"` // 新增字段，用于存储上次重置的时间
}

type Comparison struct {
	Category  string  `json:"category"`  // 比较的种类
	Limit     float64 `json:"limit"`     // 上限值
	Threshold float64 `json:"threshold"` // 阈值
	Ratio     float64 `json:"ratio"`     // 比率
}

type TelegramMessage struct {
	ThresholdStatus bool   `json:"threshold_status"`
	RatioStatus     bool   `json:"ratio_status"`
	Token           string `json:"token"`
	ChatID          string `json:"chat_id"`
}

type Message struct {
	Telegram TelegramMessage `json:"telegram"`
}

type Config struct {
	Device     string     `json:"device"`
	Interface  string     `json:"interface"`
	Interval   int        `json:"interval"`
	StartDay   int        `json:"start_day"` // 统计起始日期
	Statistics Statistics `json:"statistics"`
	Comparison Comparison `json:"comparison"`
	Message    Message    `json:"message"`
}

const bytesToGB = 1024 * 1024 * 1024

// Read the /proc/net/dev file to get network statistics for a specific interface
func readNetworkStats(iface string) (NetStats, error) {
	file, err := os.Open("/proc/net/dev")
	if err != nil {
		return NetStats{}, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, iface+":") {
			fields := strings.Fields(line)
			receiveBytes, _ := strconv.ParseUint(fields[1], 10, 64)
			transmitBytes, _ := strconv.ParseUint(fields[9], 10, 64)

			return NetStats{ReceiveBytes: receiveBytes, TransmitBytes: transmitBytes}, nil
		}
	}

	return NetStats{}, fmt.Errorf("interface %s not found", iface)
}

// LoadConfig loads the config from the JSON file
func loadConfig(configFilePath string) (Config, error) {
	var config Config
	data, err := os.ReadFile(configFilePath)
	if err != nil {
		if os.IsNotExist(err) {
			return config, nil // Return default config if the file doesn't exist
		}
		return config, err
	}
	err = json.Unmarshal(data, &config)
	return config, err
}

// SaveConfig saves the config to the JSON file
func saveConfig(configFilePath string, config Config) error {
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(configFilePath, data, 0644)
}

// Check if the statistics need to be reset based on the start_day and current date
func checkReset(config *Config) bool {
	currentTime := time.Now()

	// Parse the last reset time from the config
	lastReset, err := time.Parse("2006-01-02", config.Statistics.LastReset)
	if err != nil {
		// If there's an error parsing the last reset, assume we need to reset
		return true
	}

	// Calculate the number of days in the current month
	firstOfMonth := time.Date(currentTime.Year(), currentTime.Month(), 1, 0, 0, 0, 0, time.Local)
	nextMonth := firstOfMonth.AddDate(0, 1, 0)          // First day of next month
	lastDayOfMonth := nextMonth.AddDate(0, 0, -1).Day() // Get the last day of current month

	// If start_day is greater than the last day of this month, adjust it to the last day
	resetDay := config.StartDay
	if resetDay > lastDayOfMonth {
		resetDay = lastDayOfMonth
	}

	// Calculate the reset date for the current month
	resetDate := time.Date(currentTime.Year(), currentTime.Month(), resetDay, 0, 0, 0, 0, time.Local)

	// If the last reset was before the current reset date and now is after or on the reset date, reset statistics
	if lastReset.Before(resetDate) && currentTime.After(resetDate) {
		return true
	}

	return false
}

// Reset statistics and also reset the Telegram status flags
// func resetStatistics(config *Config) {
func resetStatistics(config *Config, configFilePath string) {
	//fmt.Println("Resetting statistics and Telegram statuses for new month period")
	// Reset statistics
	config.Statistics.TotalReceive = 0
	config.Statistics.TotalTransmit = 0

	// Reset the last reset date
	config.Statistics.LastReset = time.Now().Format("2006-01-02")

	// Reset Telegram status flags
	config.Message.Telegram.ThresholdStatus = false
	config.Message.Telegram.RatioStatus = false

	// Save the reset config
	//err := saveConfig("config.json", *config)
	err := saveConfig(configFilePath, *config)
	if err != nil {
		fmt.Printf("Failed to save config after reset in resetStatistics: %v\n", err)
	}
}

// Send a message to Telegram via Bot API
func sendTelegramMessage(token, chatID, message, device string) error {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", token)

	body := map[string]string{
		"chat_id": chatID,
		"text":    fmt.Sprintf("[%s] %s", device, message),
	}
	jsonBody, _ := json.Marshal(body)

	_, err := http.Post(url, "application/json", bytes.NewBuffer(jsonBody))
	if err != nil {
		return fmt.Errorf("failed to send message to Telegram: %v", err)
	}

	return nil
}

// Perform comparison based on category and thresholds
// func performComparison(config *Config) error {
func performComparison(config *Config, configFilePath string) error {
	var valueInGB float64

	switch config.Comparison.Category {
	case "download":
		valueInGB = float64(config.Statistics.TotalReceive) / bytesToGB
	case "upload":
		valueInGB = float64(config.Statistics.TotalTransmit) / bytesToGB
	case "upload+download":
		valueInGB = float64(config.Statistics.TotalReceive+config.Statistics.TotalTransmit) / bytesToGB
	default:
		return fmt.Errorf("invalid comparison category: %s", config.Comparison.Category)
	}

	thresholdLimit := config.Comparison.Limit * config.Comparison.Threshold
	ratioLimit := config.Comparison.Limit * config.Comparison.Ratio

	// Compare with threshold and send message if needed
	if valueInGB >= thresholdLimit && !config.Message.Telegram.ThresholdStatus {
		//fmt.Println("大于阈值，发送消息")
		message := fmt.Sprintf("流量提醒：当前使用量为 %.2f GB，超过了设置的%.0f%%阈值", valueInGB, config.Comparison.Threshold*100)
		err := sendTelegramMessage(config.Message.Telegram.Token, config.Message.Telegram.ChatID, message, config.Device)
		if err != nil {
			fmt.Printf("Failed to compare thresholdLimit: %v\n", err)
		} else {
			//fmt.Println("阈值发送成功，修改状态")
			config.Message.Telegram.ThresholdStatus = true // Mark threshold status as true after sending

			// Save the updated config to the file
			//err = saveConfig("config.json", *config)
			err = saveConfig(configFilePath, *config)
			if err != nil {
				fmt.Printf("Failed to save config in thresholdLimit: %v\n", err)
			}
		}
	}

	// Check for shutdown warning and send message if needed
	if valueInGB >= ratioLimit && !config.Message.Telegram.RatioStatus {
		//fmt.Println("大于比率，发送消息")
		message := fmt.Sprintf("关机警告：当前使用量 %.2f GB，超过了限制的%.0f%%，即将关机！", valueInGB, config.Comparison.Ratio*100)
		err := sendTelegramMessage(config.Message.Telegram.Token, config.Message.Telegram.ChatID, message, config.Device)
		if err != nil {
			fmt.Printf("Failed to compare ratioLimit: %v\n", err)
		} else {
			//fmt.Println("警告发送成功，修改状态")
			config.Message.Telegram.RatioStatus = true // Mark ratio status as true after sending

			// Save the updated config to the file
			//err = saveConfig("config.json", *config)
			err = saveConfig(configFilePath, *config)
			if err != nil {
				fmt.Printf("Failed to save config in ratioLimit: %v\n", err)
			}

			// Wait for 30 seconds before shutting down
			time.Sleep(30 * time.Second)

			// Execute shutdown command
			cmd := exec.Command("shutdown", "-h", "now")
			err := cmd.Run()
			if err != nil {
				fmt.Printf("Failed to execute shutdown command: %v\n", err)
			}
		}
	}

	return nil
}

func main() {
	// Parse the command-line flag for the config file path
	configFilePath := flag.String("c", "/path/to/config.json", "Path to the config JSON file")
	flag.Parse()

	// Load the config file (or create a new one if not exists)
	config, err := loadConfig(*configFilePath)
	if err != nil {
		fmt.Printf("Failed to load config in main: %v\n", err)
		return
	}

	// Set the interface name (if not already set in config)
	if config.Interface == "" {
		config.Interface = "eth0" // Default to eth0, you can change it or make it configurable
	}

	// Check if the interface exists
	_, err = readNetworkStats(config.Interface)
	if err != nil {
		fmt.Printf("Error checking interface existing: %v\n", err)
		return
	}

	// Use the interval defined in config.json
	interval := config.Interval
	if interval == 0 {
		interval = 600 // Default to 600 seconds if not specified
	}

	for {
		// Check if the statistics need to be reset based on the start day
		if checkReset(&config) {
			//resetStatistics(&config) // Reset statistics and telegram statuses
			resetStatistics(&config, *configFilePath)
		}

		stats, err := readNetworkStats(config.Interface)
		if err != nil {
			fmt.Printf("Error reading network stats: %v\n", err)
			time.Sleep(time.Duration(interval) * time.Second)
			continue
		}

		// Check for system reboot by comparing previous and current values
		if stats.ReceiveBytes < config.Statistics.LastReceive {
			// System reboot detected for receive bytes
			config.Statistics.TotalReceive += config.Statistics.LastReceive
		}
		if stats.TransmitBytes < config.Statistics.LastTransmit {
			// System reboot detected for transmit bytes
			config.Statistics.TotalTransmit += config.Statistics.LastTransmit
		}

		// Update the total counts
		config.Statistics.TotalReceive += stats.ReceiveBytes - config.Statistics.LastReceive
		config.Statistics.TotalTransmit += stats.TransmitBytes - config.Statistics.LastTransmit

		// Save the current stats as the "last" stats for the next check
		config.Statistics.LastReceive = stats.ReceiveBytes
		config.Statistics.LastTransmit = stats.TransmitBytes

		// Save the updated config to the file
		err = saveConfig(*configFilePath, config)
		if err != nil {
			fmt.Printf("Failed to update stats to config: %v\n", err)
		}

		// Print the stats, in GB units for better readability
		// fmt.Printf("Total Receive: %.2f GB, Total Transmit: %.2f GB\n",
		// 	float64(config.Statistics.TotalReceive)/bytesToGB,
		// 	float64(config.Statistics.TotalTransmit)/bytesToGB)

		// Perform comparison and check for warnings
		//err = performComparison(&config)
		err = performComparison(&config, *configFilePath)
		if err != nil {
			fmt.Printf("Comparison error: %v\n", err)
		}

		// Wait for the next interval
		time.Sleep(time.Duration(interval) * time.Second)
	}
}
