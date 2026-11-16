package sqlite

import "encoding/json"

// Private closed schemas project existing document V1 facts and original positions.
func webQueryRequestSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"index":{"type":"integer","minimum":0,"maximum":7}},"required":["index"],"additionalProperties":false}`)
}
func webQueryReplySchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"result":{"type":"object","properties":{"provider":{"type":"string","maxLength":33554432},"query":{"type":"string","maxLength":33554432},"text":{"type":"string","maxLength":33554432},"sources":{"type":"array","items":{"type":"object","properties":{"url":{"type":"string","maxLength":33554432},"title":{"type":"string","maxLength":33554432}},"required":["url"],"additionalProperties":false},"maxItems":20},"attempts":{"type":"array","items":{"type":"object","properties":{"provider":{"type":"string","maxLength":33554432},"outcome":{"type":"string","maxLength":33554432}},"required":["provider","outcome"],"additionalProperties":false},"maxItems":4},"retrieved_at":{"type":"string","maxLength":33554432},"freshness":{"type":"string","maxLength":33554432},"model_usage":{"type":"object","properties":{"input_tokens":{"type":"integer"},"output_tokens":{"type":"integer"},"cache_read_tokens":{"type":"integer"},"cache_write_tokens":{"type":"integer"}},"required":["input_tokens","output_tokens","cache_read_tokens","cache_write_tokens"],"additionalProperties":false},"cost":{"type":"string","maxLength":33554432},"untrusted":{"type":"boolean"}},"required":["provider","query","text","sources","attempts","retrieved_at","freshness","cost","untrusted"],"additionalProperties":false},"error_code":{"type":"string","maxLength":128}},"required":["result","error_code"],"additionalProperties":false}`)
}
func webPageRequestSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"slot":{"type":"integer","minimum":0,"maximum":7},"query":{"type":"integer","minimum":-1,"maximum":7},"source":{"type":"integer","minimum":0,"maximum":19}},"required":["slot","query","source"],"additionalProperties":false}`)
}
func webPageReplySchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"page":{"type":"object","properties":{"media_type":{"type":"string","maxLength":33554432},"url":{"type":"string","maxLength":33554432},"title":{"type":"string","maxLength":33554432},"text":{"type":"string","maxLength":33554432},"retrieved_at":{"type":"string","maxLength":33554432},"freshness":{"type":"string","maxLength":33554432},"untrusted":{"type":"boolean"}},"required":["url","text","retrieved_at","freshness","untrusted"],"additionalProperties":false},"error_code":{"type":"string","maxLength":128}},"required":["page","error_code"],"additionalProperties":false}`)
}
func webAnswerRequestSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"index":{"type":"integer","minimum":0,"maximum":7},"system":{"type":"string","maxLength":65536},"user":{"type":"string","maxLength":33554432}},"required":["index","system","user"],"additionalProperties":false}`)
}
func webAnswerReplySchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"text":{"type":"string","maxLength":33554432},"error_code":{"type":"string","maxLength":128}},"required":["text","error_code"],"additionalProperties":false}`)
}
