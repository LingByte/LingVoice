package text

import (
	"testing"

	"github.com/LingByte/LingVoice/pkg/utils/base"
)

func TestQiniuGetTextCensor(t *testing.T) {
	accessKey := base.GetEnv("QINIU_ACCESS_KEY")
	secretKey := base.GetEnv("QINIU_SECRET_KEY")
	if accessKey == "" || secretKey == "" {
		t.Skipf("not found QINIU_ACCESS_KEY or QINIU_SECRET_KEY")
	}
	textCensor, err := GetTextCensor(KindQiNiu)
	if err != nil {
		t.Error(err)
	}
	result, err := textCensor.CensorText("hello world")
	if err != nil {
		t.Error(err)
	}
	if result != nil {
		t.Logf("Suggestion: %s, Label: %s, Score: %.4f, Msg: %s", result.Suggestion, result.Label, result.Score, result.Msg)
	}
}

func TestQCloudGetTextCensor(t *testing.T) {
	secretID := base.GetEnv("QCLOUD_SECRET_ID")
	secretKey := base.GetEnv("QCLOUD_SECRET_KEY")
	if secretID == "" || secretKey == "" {
		t.Skipf("not found QCLOUD_SECRET_ID or QCLOUD_SECRET_KEY")
	}
	textCensor, err := GetTextCensor(KindQCloud)
	if err != nil {
		t.Error(err)
	}
	result, err := textCensor.CensorText("hello world")
	if err != nil {
		t.Error(err)
	}
	if result != nil {
		t.Logf("Suggestion: %s, Label: %s, Score: %.4f, Msg: %s", result.Suggestion, result.Label, result.Score, result.Msg)
	}
}

func TestAliyunGetTextCensor(t *testing.T) {
	accessKeyID := base.GetEnv("ALIYUN_ACCESS_KEY_ID")
	accessKeySecret := base.GetEnv("ALIYUN_ACCESS_KEY_SECRET")
	if accessKeyID == "" || accessKeySecret == "" {
		t.Skipf("not found ALIYUN_ACCESS_KEY_ID or ALIYUN_ACCESS_KEY_SECRET")
	}
	textCensor, err := GetTextCensor(KindAliyun)
	if err != nil {
		t.Error(err)
	}
	result, err := textCensor.CensorText("hello world")
	if err != nil {
		t.Error(err)
	}
	if result != nil {
		t.Logf("Suggestion: %s, Label: %s, Score: %.4f, Msg: %s", result.Suggestion, result.Label, result.Score, result.Msg)
	}
}
