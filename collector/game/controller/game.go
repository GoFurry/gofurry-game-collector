package controller

import (
	"fmt"
	"time"

	"github.com/GoFurry/gofurry-game-collector/collector/game/service"
	"github.com/GoFurry/gofurry-game-collector/common/log"
	cs "github.com/GoFurry/gofurry-game-collector/common/service"
	"github.com/GoFurry/gofurry-game-collector/roof/env"
)

type gameApi struct{}

var GameApi *gameApi

func init() {
	GameApi = &gameApi{}
}

// 初始化 Game 采集模块
func (api *gameApi) InitGameCollection() {
	defer func() {
		if err := recover(); err != nil {
			log.Error("receive InitGameCollection recover: ", err)
		}
	}()
	fmt.Println("Game 模块初始化开始...")

	//初始化后执行一次 Ping
	go service.GetGameService().Collect()
	go service.GetGameService().CollectCurrentPlayers()

	// 定时任务执行 Ping
	cs.AddCronJob(time.Duration(env.GetServerConfig().Collector.Game.GameInterval)*time.Hour, service.GetGameService().Collect)
	cs.AddCronJob(time.Duration(env.GetServerConfig().Collector.Game.GamePlayerInterval)*time.Hour, service.GetGameService().CollectCurrentPlayers)

	fmt.Println("Game 模块初始化结束...")
}
