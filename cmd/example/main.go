package main

import (
	"context"
	"fmt"
	"log"

	"github.com/milvus-io/milvus-sdk-go/v2/client"
)

func main() {
	// ======================
	// 在这里修改你的 Milvus 地址
	// ======================
	milvusAddr := "111.229.173.117:7075" // 默认 gRPC 端口 19530

	// 1. 创建连接（底层自动使用 gRPC）
	ctx := context.Background()
	c, err := client.NewClient(ctx, client.Config{
		Address: milvusAddr,
	})
	if err != nil {
		log.Fatalf("❌ 连接 Milvus 失败: %v", err)
	}
	defer c.Close()

	fmt.Printf("✅ 成功连接 Milvus: %s\n", milvusAddr)

	// 2. 获取服务版本
	version, err := c.GetVersion(ctx)
	if err != nil {
		log.Fatalf("❌ 获取版本失败: %v", err)
	}
	fmt.Printf("✅ Milvus 版本: %s\n", version)

	// 3. 检查服务状态
	ready, err := c.CheckHealth(ctx)
	if err != nil {
		log.Fatalf("❌ 健康检查失败: %v", err)
	}
	fmt.Printf("✅ 服务状态: %v\n", ready)

	// 4. 列出所有集合
	collections, err := c.ListCollections(ctx)
	if err != nil {
		log.Fatalf("❌ 获取集合列表失败: %v", err)
	}
	fmt.Printf("✅ 当前集合数量: %d\n", len(collections))

	fmt.Println("\n🎉 Milvus 运行完全正常！")
}

//
//package main
//
//import (
//	"context"
//	"fmt"
//	"log"
//	"time"
//
//	"github.com/milvus-io/milvus-sdk-go/v2/client"
//	"github.com/milvus-io/milvus-sdk-go/v2/entity"
//)
//
//func main() {
//	// 你的配置（已经正常了）
//	addr := "111.229.173.117:7075"
//	collectionName := "test_go_vector"
//
//	// ======================
//	// 1. 连接（加长超时时间）
//	// ======================
//	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
//	defer cancel()
//
//	c, err := client.NewClient(ctx, client.Config{
//		Address: addr,
//	})
//	if err != nil {
//		log.Fatalf("连接失败: %v", err)
//	}
//	defer c.Close()
//
//	// 检查版本
//	ver, _ := c.GetVersion(ctx)
//	fmt.Printf("✅ Milvus 连接成功 | 版本: %s\n", ver)
//
//	// ======================
//	// 2. 创建集合
//	// ======================
//	schema := &entity.Schema{
//		CollectionName: collectionName,
//		Fields: []*entity.Field{
//			{
//				Name:       "id",
//				DataType:   entity.FieldTypeInt64,
//				PrimaryKey: true,
//				AutoID:     false,
//			},
//			{
//				Name:       "vector",
//				DataType:   entity.FieldTypeFloatVector,
//				TypeParams: map[string]string{"dim": "3"},
//			},
//		},
//	}
//
//	// 先检查集合是否存在，存在就删除，避免报错
//	has, err := c.HasCollection(ctx, collectionName)
//	if err != nil {
//		log.Fatal(err)
//	}
//	if has {
//		_ = c.DropCollection(ctx, collectionName)
//		fmt.Println("✅ 已删除旧集合，重新创建")
//	}
//
//	err = c.CreateCollection(ctx, schema, 1)
//	if err != nil {
//		log.Fatalf("创建集合失败: %v", err)
//	}
//	fmt.Println("✅ 集合创建成功")
//
//	// ======================
//	// 3. 创建索引（必须）
//	// ======================
//	idx, _ := entity.NewIndexFlat(entity.L2)
//	err = c.CreateIndex(ctx, collectionName, "vector", idx, false)
//	if err != nil {
//		log.Fatal(err)
//	}
//	fmt.Println("✅ 索引创建成功")
//
//	// ======================
//	// 4. 加载集合（必须）
//	// ======================
//	err = c.LoadCollection(ctx, collectionName, false)
//	if err != nil {
//		log.Fatal(err)
//	}
//	fmt.Println("✅ 集合加载完成")
//
//	// ======================
//	// 5. 插入向量（秒成功）
//	// ======================
//	ids := []int64{1, 2, 3}
//	vectors := [][]float32{
//		{0.1, 0.2, 0.3},
//		{0.4, 0.5, 0.6},
//		{0.7, 0.8, 0.9},
//	}
//
//	idColumn := entity.NewColumnInt64("id", ids)
//	vectorColumn := entity.NewColumnFloatVector("vector", 3, vectors)
//
//	_, err = c.Insert(ctx, collectionName, "", idColumn, vectorColumn)
//	if err != nil {
//		log.Fatalf("插入失败: %v", err)
//	}
//
//	fmt.Println("\n🎉🎉🎉 全部成功！")
//	fmt.Println("👉 存入 Milvus 的是：向量数组，不是文件")
//	fmt.Println("👉 通信方式：gRPC")
//}
