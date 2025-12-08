package dao

import (
	"github.com/GoFurry/gofurry-game-collector/collecter/game/models"
	"github.com/GoFurry/gofurry-game-collector/common"
	"github.com/GoFurry/gofurry-game-collector/common/abstract"
)

var newGamePlayerDao = new(gamePlayerDao)

func init() {
	newGamePlayerDao.Init()
	newGamePlayerDao.Mode = models.GfgGamePlayerCount{}
}

type gamePlayerDao struct{ abstract.Dao }

func GetGamePlayerDao() *gamePlayerDao { return newGamePlayerDao }

// 获取在线人数记录数量
func (dao gamePlayerDao) GetPlayerCountByID(id int64) (cnt int64, gfError common.GFError) {
	db := dao.Gm.Table(models.TableNameGfgGamePlayerCount).Where("game_id=?", id).Count(&cnt)
	if err := db.Error; err != nil {
		return 0, common.NewDaoError(err.Error())
	}
	return
}

// 获取最后一条在线人数记录的 ID
func (dao gamePlayerDao) GetLastRecordByID(id int64, skipCount int) (recordId []int64, gfError common.GFError) {
	db := dao.Gm.Table(models.TableNameGfgGamePlayerCount).Select("id").Where("game_id=?", id)
	db = db.Order("create_time ASC")
	db = db.Offset(skipCount).Find(&recordId)
	if err := db.Error; err != nil {
		return nil, common.NewDaoError(err.Error())
	}
	return recordId, nil
}
