package lexical

import "testing"

func TestLocalRelevanceRequiresSubjectAndHandlesChinese(t *testing.T) {
	query := Terms("你给我调研一下，现在 grokbot 怎么用来赚钱，给我一个 roadmap")
	if Relevance(query, "我没看到 intelligent ui 的展示") != 0 {
		t.Fatal("unrelated UI report matched")
	}
	if Relevance(query, "Grokbot monetization roadmap") < .25 {
		t.Fatal("subject and intent lost")
	}
	if Relevance(Terms("无糖绿茶"), "用户偏好无糖绿茶") != 1 {
		t.Fatal("Chinese segmentation lost full match")
	}
}
