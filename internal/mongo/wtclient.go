package mongo

import (
	"context"
	"fmt"
	"github.com/rs/zerolog/log"
	"github.com/telekom/quasar/internal/config"
	"github.com/telekom/quasar/internal/utils"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"strings"
	"sync"
)

type WriteThroughClient struct {
	client *mongo.Client
	config *config.MongoConfiguration
	ctx    context.Context
	mutex  sync.Mutex
}

func NewWriteTroughClient(config *config.MongoConfiguration) *WriteThroughClient {
	var client, err = mongo.Connect(context.Background(), options.Client().ApplyURI(config.Uri))
	if err != nil {
		log.Fatal().Err(err).Msg("Could not connect to MongoDB")
	}

	if err := client.Ping(context.Background(), nil); err != nil {
		log.Fatal().Err(err).Msg("Could not reach MongoDB")
	}

	return &WriteThroughClient{
		client: client,
		config: config,
		ctx:    context.Background(),
	}
}

func (c *WriteThroughClient) EnsureIndexes() {
	for _, index := range c.config.Indexes {
		var resource = config.Current.Kubernetes.GetGroupVersionResource()
		var colName = strings.ToLower(fmt.Sprintf("%s.%s.%s", resource.Resource, resource.Group, resource.Version))
		var col = c.client.Database(c.config.Database).Collection(colName)

		indexName, err := col.Indexes().CreateOne(c.ctx, index.ToIndexModel())
		if err != nil {
			log.Error().Fields(map[string]any{
				"index": indexName,
			}).Err(err).Msg("Could not create index")
			continue
		}
		log.Debug().Fields(map[string]any{
			"index": indexName,
		}).Msg("Created index")
	}
}

func (c *WriteThroughClient) Add(obj *unstructured.Unstructured) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	var opts = options.Update().SetUpsert(true)
	filter, update := c.createFilterAndUpdate(obj)
	_, err := c.getCollection(obj).UpdateOne(c.ctx, filter, update, opts)
	if err != nil {
		log.Warn().Fields(map[string]any{
			"_id": obj.GetUID(),
		}).Err(err).Msg("Could not add object to MongoDB")
		return
	}

	log.Debug().Fields(utils.CreateFieldsForOp("wt-add", obj)).Msg("Object added to MongoDB")
}

func (c *WriteThroughClient) Update(obj *unstructured.Unstructured) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	var opts = options.Update().SetUpsert(false)
	filter, update := c.createFilterAndUpdate(obj)
	_, err := c.getCollection(obj).UpdateOne(c.ctx, filter, update, opts)
	if err != nil {
		log.Warn().Fields(map[string]any{
			"_id": obj.GetUID(),
		}).Err(err).Msg("Could not update object to MongoDB")
		return
	}

	log.Debug().Fields(utils.CreateFieldsForOp("wt-update", obj)).Msg("Object updated in MongoDB")
}

func (c *WriteThroughClient) Delete(obj *unstructured.Unstructured) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	filter, _ := c.createFilterAndUpdate(obj)

	_, err := c.getCollection(obj).DeleteOne(c.ctx, filter)
	if err != nil {
		log.Warn().Fields(map[string]any{
			"_id": obj.GetUID(),
		}).Err(err).Msg("Could not delete object from MongoDB")
		return
	}

	log.Debug().Fields(utils.CreateFieldsForOp("wt-delete", obj)).Msg("Object deleted from MongoDB")
}

func (*WriteThroughClient) createFilterAndUpdate(obj *unstructured.Unstructured) (bson.M, bson.D) {
	var objCopy = obj.DeepCopy().Object
	objCopy["_id"] = obj.GetUID()
	return bson.M{"_id": obj.GetUID()}, bson.D{{"$set", objCopy}}
}

func (c *WriteThroughClient) getCollection(obj *unstructured.Unstructured) *mongo.Collection {
	return c.client.Database(c.config.Database).Collection(utils.GetGroupVersionId(obj))
}
