package dao

import (
	"github.com/GoFurry/gofurry-game-collector/collecter/game/models"
	"github.com/GoFurry/gofurry-game-collector/common"
	"github.com/GoFurry/gofurry-game-collector/common/abstract"
)

var newGameDao = new(gameDao)

func init() {
	newGameDao.Init()
	newGameDao.Mode = models.GfgGameRecord{}
}

type gameDao struct{ abstract.Dao }

func GetGameDao() *gameDao { return newGameDao }

// 获取游戏列表
func (dao gameDao) GetGameList() ([]models.GameID, common.GFError) {
	var res []models.GameID
	db := dao.Gm.Table(models.TableNameGfgGame).Select("id, appid")
	db.Find(&res)
	if err := db.Error; err != nil {
		return nil, common.NewDaoError(err.Error())
	}
	return res, nil
}

// 获取游戏记录
func (dao gameDao) GetGameRecordByGameIDAndLang(gameID int64, lang string) (models.GfgGameRecord, common.GFError) {
	var res models.GfgGameRecord
	db := dao.Gm.Table(models.TableNameGfgGameRecord).Where("game_id=? AND lang=?", gameID, lang)
	db.Take(&res)
	if err := db.Error; err != nil {
		return res, common.NewDaoError(err.Error())
	}
	return res, nil
}
