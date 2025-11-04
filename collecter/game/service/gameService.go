package service

import (
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/GoFurry/gofurry-game-collector/collecter/game/dao"
	"github.com/GoFurry/gofurry-game-collector/collecter/game/models"
	"github.com/GoFurry/gofurry-game-collector/common"
	"github.com/GoFurry/gofurry-game-collector/common/log"
	cm "github.com/GoFurry/gofurry-game-collector/common/models"
	cs "github.com/GoFurry/gofurry-game-collector/common/service"
	"github.com/GoFurry/gofurry-game-collector/common/util"
	"github.com/GoFurry/gofurry-game-collector/roof/env"
	"github.com/sourcegraph/conc/pool"
	"github.com/tidwall/gjson"
)

type gameService struct{}

var gameSingleton = new(gameService)

func GetGameService() *gameService { return gameSingleton }

var gameThread = pool.New().WithMaxGoroutines(env.GetServerConfig().Collector.Game.GameThread)
var gameRWLock sync.RWMutex
var wg sync.WaitGroup

// 设置请求头，明确指定语言为中文
var headersMap = map[string]string{
	"User-Agent":      common.USER_AGENT,
	"Accept-Language": common.ACCEPT_LANGUAGE_CN,
}

// 采集的国区代码
var langList = []string{"CN", "HK", "US"}

// 游戏模块采集部分
func (s gameService) Collect() {
	// 每次采集都查寻数据库 保证热更新
	gameList, err := addAllGameToList()
	if err != nil {
		log.Error("receive InitGameCollection recover: ", err)
	}

	log.Info("Game 采集开始")
	// 遍历 Game 列表
	// 游戏信息
	for _, v := range gameList {
		wg.Add(1)
		gameThread.Go(startGameCollect(v))
	}
	// 游戏更新信息
	for _, v := range gameList {
		wg.Add(1)
		gameThread.Go(startGameNewsCollect(v))
	}
	// 等待所有 Game 采集完毕
	wg.Wait()
	log.Info("Game 采集结束")

}

// 开始游戏记录采集
func startGameCollect(gameID models.GameID) func() {
	return func() {
		defer func() {
			if err := recover(); err != nil {
				log.Error("receive startGameCollect recover: ", err)
			}
		}()
		defer wg.Done() // 确保线程结束时组数减少

		// 执行采集获取结果
		priceRes, infoRes := performGameCollect(gameID)

		// 存储结构
		var dbRecordCN, dbRecordEN models.GfgGameRecord
		var redisRecordCN, redisRecordEN models.GameSaveModel
		var priceList []models.PriceModel

		dbRecordCN.ID, dbRecordEN.ID = util.GenerateId(), util.GenerateId()
		dbRecordCN.GameID, dbRecordEN.GameID = gameID.ID, gameID.ID
		dbRecordCN.Lang, dbRecordEN.Lang = "zh", "en"

		// 处理价格结果
		if _, exist := priceRes["free"]; exist {
			priceList = append(priceList, models.PriceModel{
				Price:   "free",
				Country: "免费",
			})
			dbRecordCN.Initial, dbRecordEN.Initial = 0, 0
			dbRecordCN.Final, dbRecordEN.Final = 0, 0
			dbRecordCN.Discount, dbRecordEN.Discount = 0, 0
			redisRecordCN.Price.Initial, redisRecordCN.Price.Final, redisRecordCN.Price.DiscountPercent,
				redisRecordCN.Price.InitialFormatted, redisRecordCN.Price.FinalFormatted = 0, 0, 0, "免费", "免费"

			redisRecordEN.Price.Initial, redisRecordEN.Price.Final, redisRecordEN.Price.DiscountPercent,
				redisRecordEN.Price.InitialFormatted, redisRecordEN.Price.FinalFormatted = 0, 0, 0, "free", "free"

			redisRecordCN.Price.Currency, redisRecordEN.Price.Currency = "CNY", "USD"
		} else {
			for k, v := range priceRes {
				priceList = append(priceList, models.PriceModel{
					Price:   v.FinalFormatted,
					Country: k,
				})
				switch k {
				case "CN":
					dbRecordCN.Initial = v.Initial
					dbRecordCN.Final = v.Final
					dbRecordCN.Discount = v.DiscountPercent

					redisRecordCN.Price = v
				case "US":
					dbRecordEN.Initial = v.Initial
					dbRecordEN.Final = v.Final
					dbRecordEN.Discount = v.DiscountPercent

					redisRecordEN.Price = v
				}
			}
		}
		priceListJson, jsonErr := json.Marshal(priceList)
		if jsonErr != nil {
			log.Error("marshal priceList error: ", jsonErr)
		}
		priceListStr := string(priceListJson)
		dbRecordCN.PriceList, dbRecordEN.PriceList, redisRecordCN.PriceList, redisRecordEN.PriceList = priceListStr, priceListStr, priceListStr, priceListStr

		// 处理其他信息
		for k, v := range infoRes {
			switch k {
			case "CN":
				// 数据库部分
				dbRecordCN.Language = v["supported_languages"].(string)               // 支持的语言
				dbRecordCN.Developer = strings.Join(v["developers"].([]string), ", ") // 开发商
				dbRecordCN.Publisher = strings.Join(v["publishers"].([]string), ", ") // 发行商
				dbRecordCN.Cover = v["header_image"].(string)                         // 封面图
				dbRecordCN.Info = v["short_description"].(string)                     // 概述
				// 发行时间
				if v["release_date"].(models.SteamAppRelease).ComingSoon {
					dbRecordCN.ReleaseDate = "即将推出"
				} else {
					dbRecordCN.ReleaseDate = v["release_date"].(models.SteamAppRelease).Date
				}
				// 支持平台
				platform := v["platforms"].(models.SteamAppPlatform)
				var platforms []string
				if platform.Windows {
					platforms = append(platforms, "windows")
				}
				if platform.Mac {
					platforms = append(platforms, "mac")
				}
				if platform.Linux {
					platforms = append(platforms, "linux")
				}
				dbRecordCN.Platform = strings.Join(platforms, ", ")
				// redis 部分
				redisRecordCN.Support = v["support_info"].(models.SteamAppSupport)         // 开发商联系方式
				redisRecordCN.Screenshots = v["screenshots"].([]models.SteamAppScreenshot) // 游戏图片
				redisRecordCN.Movies = v["movies"].([]models.SteamAppMovie)                // 游戏视频
				redisRecordCN.SupportedLanguages = dbRecordCN.Language                     // 支持语言
				redisRecordCN.Developers = dbRecordCN.Developer                            // 开发商
				redisRecordCN.Publishers = dbRecordCN.Publisher                            // 发行商
				redisRecordCN.HeaderImage = dbRecordCN.Cover                               // 封面图
				redisRecordCN.ShortDescription = dbRecordCN.Info                           // 概述
				redisRecordCN.Date = dbRecordCN.ReleaseDate                                // 发行日期
				redisRecordCN.Platforms = dbRecordCN.Platform                              // 支持平台
				redisRecordCN.RequiredAge = v["required_age"].(string)                     // 年龄限制
				redisRecordCN.Website = v["website"].(string)                              // 游戏官网
				redisRecordCN.ContentDescriptors = v["content_descriptors"].(string)       // 内容描述
				redisRecordCN.CollectDate = cm.LocalTime(time.Now())                       // 采集时间
			case "US":
				// 数据库部分
				dbRecordEN.Language = v["supported_languages"].(string)               // 支持的语言
				dbRecordEN.Developer = strings.Join(v["developers"].([]string), ", ") // 开发商
				dbRecordEN.Publisher = strings.Join(v["publishers"].([]string), ", ") // 发行商
				dbRecordEN.Cover = v["header_image"].(string)                         // 封面图
				dbRecordEN.Info = v["short_description"].(string)                     // 概述
				// 发行时间
				if v["release_date"].(models.SteamAppRelease).ComingSoon {
					dbRecordEN.ReleaseDate = "即将推出"
				} else {
					dbRecordEN.ReleaseDate = v["release_date"].(models.SteamAppRelease).Date
				}
				// 支持平台
				platform := v["platforms"].(models.SteamAppPlatform)
				var platforms []string
				if platform.Windows {
					platforms = append(platforms, "windows")
				}
				if platform.Mac {
					platforms = append(platforms, "mac")
				}
				if platform.Linux {
					platforms = append(platforms, "linux")
				}
				dbRecordEN.Platform = strings.Join(platforms, ", ")
				// redis 部分
				redisRecordEN.Support = v["support_info"].(models.SteamAppSupport)         // 开发商联系方式
				redisRecordEN.Screenshots = v["screenshots"].([]models.SteamAppScreenshot) // 游戏图片
				redisRecordEN.Movies = v["movies"].([]models.SteamAppMovie)                // 游戏视频
				redisRecordEN.SupportedLanguages = dbRecordCN.Language                     // 支持语言
				redisRecordEN.Developers = dbRecordCN.Developer                            // 开发商
				redisRecordEN.Publishers = dbRecordCN.Publisher                            // 发行商
				redisRecordEN.HeaderImage = dbRecordCN.Cover                               // 封面图
				redisRecordEN.ShortDescription = dbRecordCN.Info                           // 概述
				redisRecordEN.Date = dbRecordCN.ReleaseDate                                // 发行日期
				redisRecordEN.Platforms = dbRecordCN.Platform                              // 支持平台
				redisRecordEN.RequiredAge = v["required_age"].(string)                     // 年龄限制
				redisRecordEN.Website = v["website"].(string)                              // 游戏官网
				redisRecordEN.ContentDescriptors = v["content_descriptors"].(string)       // 内容描述
				redisRecordEN.CollectDate = cm.LocalTime(time.Now())                       // 采集时间
			}
		}

		// 存数据库
		enRecord, err := dao.GetGameDao().GetGameRecordByGameIDAndLang(gameID.ID, "en")
		if err != nil && err.GetMsg() == "record not found" {
			dbRecordEN.HotIndex = 0
			dao.GetGameDao().Add(&dbRecordEN)
		} else if err == nil {
			dbRecordEN.HotIndex = enRecord.HotIndex
			dbRecordEN.ID = enRecord.ID
			dao.GetGameDao().Update(enRecord.ID, &dbRecordEN)
		}
		zhRecord, err := dao.GetGameDao().GetGameRecordByGameIDAndLang(gameID.ID, "zh")
		if err != nil && err.GetMsg() == "record not found" {
			dbRecordCN.HotIndex = 0
			dao.GetGameDao().Add(&dbRecordCN)
		} else if err == nil {
			dbRecordCN.HotIndex = zhRecord.HotIndex
			dbRecordCN.ID = zhRecord.ID
			dao.GetGameDao().Update(zhRecord.ID, &dbRecordCN)
		}

		// 存 redis
		idStr := util.Int642String(gameID.ID)
		jsonResultCN, _ := json.Marshal(redisRecordCN)
		cs.SetNX("game:zh-info"+idStr, string(jsonResultCN), 12*time.Hour)     // 创建记录
		cs.SetExpire("game:zh-info"+idStr, string(jsonResultCN), 12*time.Hour) // 更新记录

		jsonResultEN, _ := json.Marshal(redisRecordEN)
		cs.SetNX("game:en-info"+idStr, string(jsonResultEN), 12*time.Hour)     // 创建记录
		cs.SetExpire("game:en-info"+idStr, string(jsonResultEN), 12*time.Hour) // 更新记录
	}
}

// 执行游戏记录采集
func performGameCollect(gameID models.GameID) (map[string]models.SteamAppPrice, map[string]map[string]any) {
	defer func() {
		if err := recover(); err != nil {
			log.Error("receive performGameCollect recover: ", err)
		}
	}()

	appidStr := util.Int642String(gameID.Appid)

	var priceRes = make(map[string]models.SteamAppPrice)
	var infoRes = make(map[string]map[string]any)

	// 请求地址
	url := `https://store.steampowered.com/api/appdetails`

	// 设置参数
	paramsMap := map[string]string{
		"appids": appidStr,
		"cc":     "",
	}

	// 采中文和英文两种版本
	nowHeaders := headersMap
	nowLang := "CN"
	for _, lang := range langList {
		// 设置采集的国区(价格)和语言
		paramsMap["cc"] = lang
		switch lang {
		case "US":
			nowHeaders["Accept-Language"] = common.ACCEPT_LANGUAGE_EN
			nowLang = "US"
		default:
			nowHeaders["Accept-Language"] = common.ACCEPT_LANGUAGE_CN
			nowLang = "CN"
		}

		if infoRes[nowLang] == nil {
			infoRes[nowLang] = make(map[string]any)
		}

		// 请求 SteamAPI
		respDataStr, httpErr := util.GetByHttpWithParams(url, headersMap, paramsMap, 10*time.Second, &env.GetServerConfig().Collector.Proxy)
		if httpErr != nil {
			log.Warn(httpErr)
			return priceRes, infoRes
		}

		// 是否免费
		isFree := gjson.Get(respDataStr, appidStr+".data.is_free").String()
		if isFree == "true" {
			// 免费
			priceRes["free"] = models.SteamAppPrice{
				Currency: "免费",
			}
		} else {
			// 不免费
			priceDataStr := gjson.Get(respDataStr, appidStr+".data.price_overview").String()
			var newPrice models.SteamAppPrice
			jsonErr := json.Unmarshal([]byte(priceDataStr), &newPrice)
			if jsonErr != nil {
				log.Error("models.SteamAppPrice json转换错误.", jsonErr)
			}
			priceRes[lang] = newPrice
		}

		// 支持的语言
		if _, exist := infoRes[nowLang]["supported_languages"]; !exist {
			tempDataStr := gjson.Get(respDataStr, appidStr+".data.supported_languages").String()
			infoRes[nowLang]["supported_languages"] = tempDataStr
		}

		// 发行日期
		if _, exist := infoRes[nowLang]["release_date"]; !exist {
			tempDataStr := gjson.Get(respDataStr, appidStr+".data.release_date").String()
			var newDate models.SteamAppRelease
			jsonErr := json.Unmarshal([]byte(tempDataStr), &newDate)
			if jsonErr != nil {
				log.Error("models.SteamAppRelease json转换错误.", jsonErr)
			}
			infoRes[nowLang]["release_date"] = newDate
		}

		// 支持平台
		if _, exist := infoRes[nowLang]["platforms"]; !exist {
			tempDataStr := gjson.Get(respDataStr, appidStr+".data.platforms").String()
			var newPlatform models.SteamAppPlatform
			jsonErr := json.Unmarshal([]byte(tempDataStr), &newPlatform)
			if jsonErr != nil {
				log.Error("models.SteamAppPlatform json转换错误.", jsonErr)
			}
			infoRes[nowLang]["platforms"] = newPlatform
		}

		// 开发商
		if _, exist := infoRes[nowLang]["developers"]; !exist {
			tempDataStr := gjson.Get(respDataStr, appidStr+".data.developers").String()
			var newDevelopers []string
			jsonErr := json.Unmarshal([]byte(tempDataStr), &newDevelopers)
			if jsonErr != nil {
				log.Error("newDevelopers json转换错误.", jsonErr)
			}
			infoRes[nowLang]["developers"] = newDevelopers
		}

		// 发行商
		if _, exist := infoRes[nowLang]["publishers"]; !exist {
			tempDataStr := gjson.Get(respDataStr, appidStr+".data.publishers").String()
			var newPublishers []string
			jsonErr := json.Unmarshal([]byte(tempDataStr), &newPublishers)
			if jsonErr != nil {
				log.Error("newPublishers json转换错误.", jsonErr)
			}
			infoRes[nowLang]["publishers"] = newPublishers
		}

		// 封面图
		if _, exist := infoRes[nowLang]["header_image"]; !exist {
			tempDataStr := gjson.Get(respDataStr, appidStr+".data.header_image").String()
			infoRes[nowLang]["header_image"] = tempDataStr
		}

		// 概述
		if _, exist := infoRes[nowLang]["short_description"]; !exist {
			tempDataStr := gjson.Get(respDataStr, appidStr+".data.short_description").String()
			infoRes[nowLang]["short_description"] = tempDataStr
		}

		// 其他信息
		// 年龄限制
		if _, exist := infoRes[nowLang]["required_age"]; !exist {
			tempDataStr := gjson.Get(respDataStr, appidStr+".data.ratings.steam_germany.required_age").String()
			infoRes[nowLang]["required_age"] = tempDataStr
		}

		// 开发商联系方式
		if _, exist := infoRes[nowLang]["support_info"]; !exist {
			tempDataStr := gjson.Get(respDataStr, appidStr+".data.support_info").String()
			var newSupport models.SteamAppSupport
			jsonErr := json.Unmarshal([]byte(tempDataStr), &newSupport)
			if jsonErr != nil {
				log.Error("models.SteamAppSupport json转换错误.", jsonErr)
			}
			infoRes[nowLang]["support_info"] = newSupport
		}

		// 游戏官网
		if _, exist := infoRes[nowLang]["website"]; !exist {
			tempDataStr := gjson.Get(respDataStr, appidStr+".data.website").String()
			infoRes[nowLang]["website"] = tempDataStr
		}

		// 内容描述
		if _, exist := infoRes[nowLang]["content_descriptors"]; !exist {
			tempDataStr := gjson.Get(respDataStr, appidStr+".data.content_descriptors.notes").String()
			infoRes[nowLang]["content_descriptors"] = tempDataStr
		}

		// 游戏图片
		if _, exist := infoRes[nowLang]["screenshots"]; !exist {
			tempDataStr := gjson.Get(respDataStr, appidStr+".data.screenshots").String()
			var newScreenshot []models.SteamAppScreenshot
			jsonErr := json.Unmarshal([]byte(tempDataStr), &newScreenshot)
			if jsonErr != nil {
				log.Error("models.SteamAppScreenshot json转换错误.", jsonErr)
			}
			infoRes[nowLang]["screenshots"] = newScreenshot
		}

		// 游戏视频
		if _, exist := infoRes[nowLang]["movies"]; !exist {
			tempDataStr := gjson.Get(respDataStr, appidStr+".data.movies").String()
			var newMovie []models.SteamAppMovie
			jsonErr := json.Unmarshal([]byte(tempDataStr), &newMovie)
			if jsonErr != nil {
				log.Error("models.SteamAppMovie json转换错误.", jsonErr)
			}
			infoRes[nowLang]["movies"] = newMovie
		}
	}

	return priceRes, infoRes
}

// 开始游戏更新公告采集
func startGameNewsCollect(gameID models.GameID) func() {
	return func() {
		defer func() {
			if err := recover(); err != nil {
				log.Error("receive startGameNewsCollect recover: ", err)
			}
		}()
		defer wg.Done() // 确保线程结束时组数减少

		// 执行采集获取结果
		cnt, cntStr := 10, "10" //采集 cnt 篇更新公告
		newsResEN, newsResCN := performGameNewsCollect(gameID, cnt, cntStr)

		for i := 0; i < cnt; i++ {
			idx := util.Int2String(i)

			saveModelEN := models.GfgGameNews{
				ID:       util.GenerateId(),
				GameID:   gameID.ID,
				Headline: newsResEN[idx].Title,
				Content:  newsResEN[idx].Contents,
				Index:    int64(i),
				PostTime: newsResEN[idx].Date,
				Author:   newsResEN[idx].Author,
				URL:      newsResEN[idx].URL,
				Total:    newsResEN[idx].Count,
				Lang:     "en",
			}

			saveModelCN := models.GfgGameNews{
				ID:       util.GenerateId(),
				GameID:   gameID.ID,
				Headline: newsResCN[idx].Title,
				Content:  newsResCN[idx].Contents,
				Index:    int64(i),
				PostTime: newsResCN[idx].Date,
				Author:   newsResCN[idx].Author,
				URL:      newsResCN[idx].URL,
				Total:    newsResCN[idx].Count,
				Lang:     "zh",
			}

			// 储存到数据库
			zhRecord, err := dao.GetGameNewsDao().GetGameNews(gameID.ID, "zh", int64(i))
			if err != nil && err.GetMsg() == "record not found" {
				saveModelCN.CreateTime = cm.LocalTime(time.Now())
				dao.GetGameNewsDao().Add(&saveModelCN)
			} else if err == nil {
				saveModelCN.CreateTime = zhRecord.CreateTime
				saveModelCN.ID = zhRecord.ID
				dao.GetGameNewsDao().Update(zhRecord.ID, &saveModelCN)
			}

			enRecord, err := dao.GetGameNewsDao().GetGameNews(gameID.ID, "en", int64(i))
			if err != nil && err.GetMsg() == "record not found" {
				saveModelEN.CreateTime = cm.LocalTime(time.Now())
				dao.GetGameNewsDao().Add(&saveModelEN)
			} else if err == nil {
				saveModelEN.CreateTime = enRecord.CreateTime
				saveModelEN.ID = enRecord.ID
				dao.GetGameNewsDao().Update(enRecord.ID, &saveModelEN)
			}

			// 储存到 redis
			idStr := util.Int642String(gameID.ID)
			jsonResultCN, _ := json.Marshal(saveModelCN)
			cs.SetNX("game:zh-news"+idStr+"-"+idx, string(jsonResultCN), 12*time.Hour)     // 创建记录
			cs.SetExpire("game:zh-news"+idStr+"-"+idx, string(jsonResultCN), 12*time.Hour) // 更新记录

			jsonResultEN, _ := json.Marshal(saveModelCN)
			cs.SetNX("game:en-news"+idStr+"-"+idx, string(jsonResultEN), 12*time.Hour)     // 创建记录
			cs.SetExpire("game:en-news"+idStr+"-"+idx, string(jsonResultEN), 12*time.Hour) // 更新记录

		}

	}
}

// 执行游戏更新公告采集
func performGameNewsCollect(gameID models.GameID, cnt int, cntStr string) (map[string]models.SteamAppNews, map[string]models.SteamAppNews) {
	defer func() {
		if err := recover(); err != nil {
			log.Error("receive performGameNewsCollect recover: ", err)
		}
	}()

	appidStr := util.Int642String(gameID.Appid)

	// 请求地址
	apiUrl := `https://api.steampowered.com/ISteamNews/GetNewsForApp/v2`             // steamAPI 仅返回英文 请求速度慢
	storeUrl := `https://store.steampowered.com/events/ajaxgetadjacentpartnerevents` // 商店API 返回语言可选 请求速度快

	// 设置参数
	apiParamsMap := map[string]string{
		"appid": appidStr,
		"count": cntStr,
	}
	storeParamsMap := map[string]string{
		"appid":        appidStr,
		"count_before": "1",
		"count_after":  cntStr,
		"lang_list":    "6_0",
	}

	var newsResEN = make(map[string]models.SteamAppNews)
	var newsResCN = make(map[string]models.SteamAppNews)

	// SteamAPI 请求英文数据
	// 请求 SteamAPI
	respDataStr, httpErr := util.GetByHttpWithParams(apiUrl, headersMap, apiParamsMap, 10*time.Second, &env.GetServerConfig().Collector.Proxy)
	if httpErr != nil {
		log.Warn("api.steampowered.com/ISteamNews/GetNewsForApp 请求失败", httpErr)
		return newsResEN, newsResCN
	}

	// 解析 cnt 篇更新公告
	for i := 0; i < cnt; i++ {
		idx := util.Int2String(i)

		// 存储结构
		nowNews := models.SteamAppNews{}

		nowNews.Title = gjson.Get(respDataStr, "appnews.newsitems."+idx+".title").String()   // 标题
		nowNews.Author = gjson.Get(respDataStr, "appnews.newsitems."+idx+".author").String() // 作者
		nowNews.URL = gjson.Get(respDataStr, "appnews.newsitems."+idx+".url").String()       // URL
		nowNews.Count = gjson.Get(respDataStr, "appnews.count").Int()                        // 更新公告数
		// 日期
		date := gjson.Get(respDataStr, "appnews.newsitems."+idx+".date").Int()
		loc, _ := time.LoadLocation("Asia/Shanghai") // 中国 CST（UTC+8）
		cstTime := time.Unix(date, 0).In(loc)
		nowNews.Date = cm.LocalTime(cstTime)
		// 内容
		content := gjson.Get(respDataStr, "appnews.newsitems."+idx+".contents").String()
		nowNews.Contents = util.BBCodeToHTML(content)

		// 存储结果
		newsResEN[idx] = nowNews
	}

	// SteamStoreAPI 请求中文数据
	respDataStr, httpErr = util.GetByHttpWithParams(storeUrl, headersMap, storeParamsMap, 10*time.Second, &env.GetServerConfig().Collector.Proxy)
	if httpErr != nil {
		log.Warn("store.steampowered.com/events/ajaxgetadjacentpartnerevents 请求失败", httpErr)
		return newsResEN, newsResCN
	}

	// 解析 cnt 篇更新公告
	for i := 0; i < cnt; i++ {
		idx := util.Int2String(i)

		// 存储结构
		nowNews := models.SteamAppNews{}

		nowNews.Author = newsResEN[idx].Author                                                       // 作者
		nowNews.URL = newsResEN[idx].URL                                                             // URL
		nowNews.Date = newsResEN[idx].Date                                                           // 日期
		nowNews.Count = newsResEN[idx].Count                                                         // 更新公告数
		nowNews.Title = gjson.Get(respDataStr, "events."+idx+".announcement_body.headline").String() // 标题
		// 内容
		content := gjson.Get(respDataStr, "events."+idx+".announcement_body.body").String()
		nowNews.Contents = util.BBCodeToHTML(content)

		// 存储结果
		newsResCN[idx] = nowNews
	}
	return newsResEN, newsResCN
}

// 添加游戏记录到采集列表
func addAllGameToList() (gameList []models.GameID, err common.GFError) {
	gameList, err = dao.GetGameDao().GetGameList()
	if err != nil {
		log.Error("receive addAllGameToList recover: ", err)
	}
	return
}
