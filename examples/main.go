// examples 展示生成后的 Repository 用法；先执行 make gen 和 make schema-apply。
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	mongoplus "github.com/kuangshp/mongo-plus"
	"github.com/kuangshp/mongo-plus/examples/query/dao"
	"github.com/kuangshp/mongo-plus/examples/query/model"
	"github.com/kuangshp/mongo-plus/examples/query/repository"
)

// UserDepartmentVO 将关联结果直接解码为业务返回结构。
type UserDepartmentVO struct {
	model.UserEntity `bson:",inline"`
	Department       *model.DepartmentEntity `bson:"department" json:"department"`
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	db, err := mongoplus.Connect(ctx, mongoplus.Config{URI: os.Getenv("MONGO_URI"), Database: "mongo_plus_demo"})
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = db.Close(closeCtx)
	}()
	userRepository := repository.NewUserRepository(db)
	departmentRepository := repository.NewDepartmentRepository(db)
	department := &model.DepartmentEntity{Name: "研发部"}
	if err = departmentRepository.Create(ctx, department); err != nil {
		log.Fatal(err)
	}
	user := &model.UserEntity{Username: fmt.Sprintf("demo_%d", time.Now().UnixNano()), Status: 1, DeptID: department.ID}
	if err = userRepository.Create(ctx, user); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("创建用户：id=%d username=%s\n", user.ID, user.Username)

	_, err = userRepository.UpdateById(ctx, user.ID, dao.UserEntity.Status.Set(0))
	if err != nil {
		log.Fatal(err)
	}
	list, total, err := userRepository.FindPageByWrapper(ctx, 1, 10, func(w *mongoplus.Wrapper) {
		w.Where(dao.UserEntity.Status.Eq(0)).Order(dao.UserEntity.ID.Desc())
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("分页：当前 %d 条，共 %d 条\n", len(list), total)

	// 主、关联集合分别使用自己的逻辑删除和租户配置，结果可直接映射到 VO。
	var joined []UserDepartmentVO
	joinedTotal, err := userRepository.WithContext(ctx).
		LeftJoin(departmentRepository, dao.UserEntity.DeptID, dao.DepartmentEntity.ID, "department").
		Where(dao.UserEntity.ID.Eq(user.ID)).
		Order(dao.UserEntity.ID.Desc()).
		ScanByPage(&joined, 1, 10)
	if err != nil {
		log.Fatal(err)
	}
	if len(joined) > 0 && joined[0].Department != nil {
		fmt.Printf("关联分页：共 %d 行，用户 %s，部门 %s\n", joinedTotal, joined[0].Username, joined[0].Department.Name)
	}
	if _, err = userRepository.DeleteById(ctx, user.ID); err != nil {
		log.Fatal(err)
	}
	exists, err := userRepository.ExistsByWrapper(ctx, func(w *mongoplus.Wrapper) { w.Where(dao.UserEntity.ID.Eq(user.ID)) })
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("逻辑删除后普通查询可见：%t\n", exists)
	if _, err = userRepository.RestoreById(ctx, user.ID); err != nil {
		log.Fatal(err)
	}
	fmt.Println("恢复成功")
}
