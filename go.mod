module github.com/LingByte/LingVoice

go 1.26.2

require (
	cloud.google.com/go/speech v1.33.0
	cloud.google.com/go/texttospeech v1.19.0
	github.com/LingByte/lingstorage-sdk-go v0.0.0-20260113030255-ff9d0501909c
	github.com/alibabacloud-go/darabonba-openapi/v2 v2.1.16
	github.com/alibabacloud-go/dysmsapi-20170525/v4 v4.1.3
	github.com/alibabacloud-go/green-20220302/v2 v2.23.0
	github.com/alibabacloud-go/tea v1.4.0
	github.com/aws/aws-sdk-go-v2/config v1.32.16
	github.com/aws/aws-sdk-go-v2/service/polly v1.57.2
	github.com/aws/aws-sdk-go-v2/service/transcribestreaming v1.34.6
	github.com/baidubce/bce-sdk-go v0.9.265
	github.com/blevesearch/bleve/v2 v2.5.7
	github.com/carlmjohnson/requests v0.25.1
	github.com/coze-dev/coze-go v0.0.0-20260408095536-f47b4f256580
	github.com/deepgram/deepgram-go-sdk v1.9.0
	github.com/gin-contrib/sessions v1.1.0
	github.com/gin-gonic/gin v1.12.0
	github.com/glebarez/sqlite v1.11.0
	github.com/go-ego/gse v1.0.2
	github.com/go-playground/validator/v10 v10.30.1
	github.com/go-redis/redis/v8 v8.11.5
	github.com/go-webauthn/webauthn v0.17.0
	github.com/golang-jwt/jwt/v5 v5.3.1
	github.com/golang/freetype v0.0.0-20170609003504-e2365dfdc4a0
	github.com/google/uuid v1.6.0
	github.com/gorilla/csrf v1.7.3
	github.com/gorilla/websocket v1.5.3
	github.com/grafana/pyroscope-go v1.2.8
	github.com/hashicorp/golang-lru/v2 v2.0.7
	github.com/joho/godotenv v1.5.1
	github.com/jung-kurt/gofpdf v1.16.2
	github.com/ledongthuc/pdf v0.0.0-20250511090121-5959a4027728
	github.com/matoous/go-nanoid v1.5.1
	github.com/milvus-io/milvus-sdk-go/v2 v2.4.2
	github.com/mozillazg/go-pinyin v0.21.0
	github.com/natefinch/lumberjack v2.0.0+incompatible
	github.com/nicksnyder/go-i18n/v2 v2.6.1
	github.com/pquerna/otp v1.5.0
	github.com/sashabaranov/go-openai v1.41.2
	github.com/shirou/gopsutil v3.21.11+incompatible
	github.com/sirupsen/logrus v1.9.4
	github.com/skip2/go-qrcode v0.0.0-20200617195104-da1b6568686e
	github.com/stretchr/testify v1.11.1
	github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/asr v1.3.69
	github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common v1.3.86
	github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/ims v1.3.85
	github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/sms v1.3.86
	github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/tms v1.3.10
	github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/vod v1.3.84
	github.com/tencentcloud/tencentcloud-speech-sdk-go v1.0.21
	github.com/xuri/excelize/v2 v2.10.1
	go.uber.org/zap v1.27.1
	golang.org/x/image v0.39.0
	golang.org/x/net v0.52.0
	golang.org/x/sys v0.43.0
	golang.org/x/text v0.36.0
	gopkg.in/yaml.v3 v3.0.1
	gorm.io/driver/mysql v1.6.0
	gorm.io/driver/postgres v1.6.0
	gorm.io/driver/sqlite v1.6.0
	gorm.io/gorm v1.31.1
)

require (
	cloud.google.com/go v0.123.0 // indirect
	cloud.google.com/go/auth v0.18.2 // indirect
	cloud.google.com/go/auth/oauth2adapt v0.2.8 // indirect
	cloud.google.com/go/compute/metadata v0.9.0 // indirect
	cloud.google.com/go/longrunning v0.9.0 // indirect
	filippo.io/edwards25519 v1.1.0 // indirect
	github.com/RoaringBitmap/roaring/v2 v2.4.5 // indirect
	github.com/alibabacloud-go/alibabacloud-gateway-spi v0.0.5 // indirect
	github.com/alibabacloud-go/debug v1.0.1 // indirect
	github.com/alibabacloud-go/endpoint-util v1.1.0 // indirect
	github.com/alibabacloud-go/openapi-util v0.1.1 // indirect
	github.com/alibabacloud-go/tea-utils/v2 v2.0.7 // indirect
	github.com/aliyun/credentials-go v1.4.5 // indirect
	github.com/aws/aws-sdk-go-v2 v1.41.6 // indirect
	github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream v1.7.9 // indirect
	github.com/aws/aws-sdk-go-v2/credentials v1.19.15 // indirect
	github.com/aws/aws-sdk-go-v2/feature/ec2/imds v1.18.22 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.4.22 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.7.22 // indirect
	github.com/aws/aws-sdk-go-v2/internal/v4a v1.4.23 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.13.8 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.13.22 // indirect
	github.com/aws/aws-sdk-go-v2/service/signin v1.0.10 // indirect
	github.com/aws/aws-sdk-go-v2/service/sso v1.30.16 // indirect
	github.com/aws/aws-sdk-go-v2/service/ssooidc v1.35.20 // indirect
	github.com/aws/aws-sdk-go-v2/service/sts v1.42.0 // indirect
	github.com/aws/smithy-go v1.25.0 // indirect
	github.com/bits-and-blooms/bitset v1.22.0 // indirect
	github.com/blevesearch/bleve_index_api v1.2.11 // indirect
	github.com/blevesearch/geo v0.2.4 // indirect
	github.com/blevesearch/go-faiss v1.0.26 // indirect
	github.com/blevesearch/go-porterstemmer v1.0.3 // indirect
	github.com/blevesearch/gtreap v0.1.1 // indirect
	github.com/blevesearch/mmap-go v1.0.4 // indirect
	github.com/blevesearch/scorch_segment_api/v2 v2.3.13 // indirect
	github.com/blevesearch/segment v0.9.1 // indirect
	github.com/blevesearch/snowballstem v0.9.0 // indirect
	github.com/blevesearch/upsidedown_store_api v1.0.2 // indirect
	github.com/blevesearch/vellum v1.1.0 // indirect
	github.com/blevesearch/zapx/v11 v11.4.2 // indirect
	github.com/blevesearch/zapx/v12 v12.4.2 // indirect
	github.com/blevesearch/zapx/v13 v13.4.2 // indirect
	github.com/blevesearch/zapx/v14 v14.4.2 // indirect
	github.com/blevesearch/zapx/v15 v15.4.2 // indirect
	github.com/blevesearch/zapx/v16 v16.2.8 // indirect
	github.com/boombuler/barcode v1.0.1-0.20190219062509-6c824513bacc // indirect
	github.com/bytedance/gopkg v0.1.3 // indirect
	github.com/bytedance/sonic v1.15.0 // indirect
	github.com/bytedance/sonic/loader v0.5.0 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/clbanning/mxj/v2 v2.7.0 // indirect
	github.com/cloudwego/base64x v0.1.6 // indirect
	github.com/cockroachdb/errors v1.9.1 // indirect
	github.com/cockroachdb/logtags v0.0.0-20211118104740-dabe8e521a4f // indirect
	github.com/cockroachdb/redact v1.1.3 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/dvonthenen/websocket v1.5.1-dyv.2 // indirect
	github.com/fatih/color v1.15.0 // indirect
	github.com/felixge/httpsnoop v1.0.4 // indirect
	github.com/fxamacker/cbor/v2 v2.9.1 // indirect
	github.com/gabriel-vasile/mimetype v1.4.12 // indirect
	github.com/getsentry/sentry-go v0.12.0 // indirect
	github.com/gin-contrib/sse v1.1.0 // indirect
	github.com/glebarez/go-sqlite v1.21.2 // indirect
	github.com/go-logr/logr v1.4.3 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/go-ole/go-ole v1.3.0 // indirect
	github.com/go-playground/locales v0.14.1 // indirect
	github.com/go-playground/universal-translator v0.18.1 // indirect
	github.com/go-sql-driver/mysql v1.8.1 // indirect
	github.com/go-viper/mapstructure/v2 v2.5.0 // indirect
	github.com/go-webauthn/x v0.2.3 // indirect
	github.com/goccy/go-json v0.10.5 // indirect
	github.com/goccy/go-yaml v1.19.2 // indirect
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/golang/protobuf v1.5.4 // indirect
	github.com/golang/snappy v1.0.0 // indirect
	github.com/google/go-tpm v0.9.8 // indirect
	github.com/google/s2a-go v0.1.9 // indirect
	github.com/googleapis/enterprise-certificate-proxy v0.3.14 // indirect
	github.com/googleapis/gax-go/v2 v2.21.0 // indirect
	github.com/gorilla/context v1.1.2 // indirect
	github.com/gorilla/schema v1.4.1 // indirect
	github.com/gorilla/securecookie v1.1.2 // indirect
	github.com/gorilla/sessions v1.4.0 // indirect
	github.com/grafana/pyroscope-go/godeltaprof v0.1.9 // indirect
	github.com/grpc-ecosystem/go-grpc-middleware v1.3.0 // indirect
	github.com/hokaccha/go-prettyjson v0.0.0-20211117102719-0474bc63780f // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/pgx/v5 v5.6.0 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/jinzhu/inflection v1.0.0 // indirect
	github.com/jinzhu/now v1.1.5 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/klauspost/compress v1.18.0 // indirect
	github.com/klauspost/cpuid/v2 v2.3.0 // indirect
	github.com/kr/pretty v0.3.1 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/leodido/go-urn v1.4.0 // indirect
	github.com/mattn/go-colorable v0.1.13 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/mattn/go-sqlite3 v1.14.22 // indirect
	github.com/milvus-io/milvus-proto/go-api/v2 v2.4.10-0.20240819025435-512e3b98866a // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/mschoch/smat v0.2.0 // indirect
	github.com/pelletier/go-toml/v2 v2.2.4 // indirect
	github.com/philhofer/fwd v1.2.0 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/quasoft/memstore v0.0.0-20191010062613-2bce066d2b0b // indirect
	github.com/quic-go/qpack v0.6.0 // indirect
	github.com/quic-go/quic-go v0.59.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/richardlehane/mscfb v1.0.6 // indirect
	github.com/richardlehane/msoleps v1.0.6 // indirect
	github.com/rogpeppe/go-internal v1.14.1 // indirect
	github.com/tidwall/gjson v1.14.4 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.0 // indirect
	github.com/tiendc/go-deepcopy v1.7.2 // indirect
	github.com/tinylib/msgp v1.6.4 // indirect
	github.com/tjfoc/gmsm v1.4.1 // indirect
	github.com/tklauser/go-sysconf v0.3.16 // indirect
	github.com/tklauser/numcpus v0.11.0 // indirect
	github.com/twitchyliquid64/golang-asm v0.15.1 // indirect
	github.com/ugorji/go/codec v1.3.1 // indirect
	github.com/vcaesar/cedar v0.30.0 // indirect
	github.com/x448/float16 v0.8.4 // indirect
	github.com/xuri/efp v0.0.1 // indirect
	github.com/xuri/nfp v0.0.2-0.20250530014748-2ddeb826f9a9 // indirect
	github.com/yusufpapurcu/wmi v1.2.4 // indirect
	go.etcd.io/bbolt v1.4.0 // indirect
	go.mongodb.org/mongo-driver/v2 v2.5.0 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc v0.61.0 // indirect
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.61.0 // indirect
	go.opentelemetry.io/otel v1.42.0 // indirect
	go.opentelemetry.io/otel/metric v1.42.0 // indirect
	go.opentelemetry.io/otel/trace v1.42.0 // indirect
	go.uber.org/multierr v1.10.0 // indirect
	golang.org/x/arch v0.23.0 // indirect
	golang.org/x/crypto v0.50.0 // indirect
	golang.org/x/oauth2 v0.36.0 // indirect
	golang.org/x/sync v0.20.0 // indirect
	golang.org/x/time v0.15.0 // indirect
	google.golang.org/api v0.274.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20260401024825-9d38bb4040a9 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260401024825-9d38bb4040a9 // indirect
	google.golang.org/grpc v1.80.0 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
	gopkg.in/ini.v1 v1.67.0 // indirect
	gopkg.in/natefinch/lumberjack.v2 v2.2.1 // indirect
	k8s.io/klog/v2 v2.110.1 // indirect
	modernc.org/libc v1.22.5 // indirect
	modernc.org/mathutil v1.5.0 // indirect
	modernc.org/memory v1.5.0 // indirect
	modernc.org/sqlite v1.23.1 // indirect
)
