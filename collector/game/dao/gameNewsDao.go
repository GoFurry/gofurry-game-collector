package dao

import (
	"github.com/GoFurry/gofurry-game-collector/collector/game/models"
	"github.com/GoFurry/gofurry-game-collector/common"
	"github.com/GoFurry/gofurry-game-collector/common/abstract"
)

var newGameNewsDao = new(gameNewsDao)

func init() {
	newGameNewsDao.Init()
	newGameNewsDao.Mode = models.GfgGameNews{}
}

type gameNewsDao struct{ abstract.Dao }

func GetGameNewsDao() *gameNewsDao { return newGameNewsDao }

// 获取游戏记录
func (dao gameNewsDao) GetGameNews(gameID int64, lang string, idx int64) (models.GfgGameNews, common.GFError) {
	var res models.GfgGameNews
	db := dao.Gm.Table(models.TableNameGfgGameNews).Where("game_id=? AND lang=? AND index=?", gameID, lang, idx)
	db.Take(&res)
	if err := db.Error; err != nil {
		return res, common.NewDaoError(err.Error())
	}
	return res, nil
}
