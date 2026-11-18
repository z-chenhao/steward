# Local transcription protocol fixture

The JSON file is unmodified stdout-file output of the official whisper.cpp v1.9.2 CLI on Darwin arm64, using the release sample jfk.wav and the English tiny model. Locations use disposable files. Raw decoder diagnostics are never shown to the product user.

| Input | SHA-256 |
| --- | --- |
| Homebrew whisper-cli 1.9.2 Darwin arm64 executable | 40bca494d49af736058eb3f33cbcebaa020eacf6d0087b623f334946e1ab2128 |
| Official release samples/jfk.wav | 59dfb9a4acb36fe2a2affc14bacbee2920ff435cb13cc314a08c13f66ba7860e |
| ggml-tiny.en.bin | 921e4cf8686fdd993dcd081a5da5b6c365bfde1162e72b08d75ac75289920b1f |

```sh
whisper-cli -m ggml-tiny.en.bin -f jfk.wav -oj -of pinned -l en -ng -np -t 2
```

The sample is approximately eleven seconds. The observed result contains one segment from 0 to 10360 milliseconds. Source material is available in the [official v1.9.2 release](https://github.com/ggml-org/whisper.cpp/tree/v1.9.2) and the [maintainer's model repository](https://huggingface.co/ggerganov/whisper.cpp/blob/main/ggml-tiny.en.bin).
