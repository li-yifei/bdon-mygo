# 可替换 Rust 搜索实现（macOS）

原生设置中的「搜索算法」提供默认算法和实验性替换算法。实验库由开发/构建流程提供，界面只展示算法选项。自定义库复制到应用数据目录（macOS 为 `~/Library/Application Support/bdon-mygo/`，Windows 为 `%LOCALAPPDATA%\bdon-mygo\Data\`）的 `engines/<sha256>.<dylib|dll|so>`，保留平台后缀；选择记录保存到设置目录的 `search-engine.json`（见 README「数据存放位置」）。发行版每个平台只打包一个引擎：实验性替换算法使用 `libournotes_ffi_experimental.*`（开发时），否则使用随应用打包的 `libournotes_ffi.*`。切换会重新加载组卡页面，刷新 Worker 的数据和能力信息。已经开始的 FFI 调用持有旧实现，结果释放也由旧库处理。库在应用进程存续期间保持加载，确保切换过程中函数指针有效。

多线程等替换实现建议保留相同导出接口和 JSON 格式，仅修改内部搜索策略。当前 macOS arm64 包要求 arm64 或 universal 动态库；库自身的动态依赖需可解析。

```c
#include <stdint.h>
#include <stddef.h>

// 所有输入均为 UTF-8、NUL 结尾字符串，调用期间有效。
char *ournotes_recommend_account_json(
    const char *dataset_path,
    const char *account_json,
    const char *request_json);
char *ournotes_dataset_info_json(const char *dataset_path);
char *ournotes_capabilities_json(void);
size_t ournotes_string_len(const char *result);
void ournotes_free_string(char *result);
```

前三个接口返回库分配的 UTF-8 JSON 字符串。`ournotes_string_len` 返回实际字节长度，排除 NUL 终止符。Go 读取结果后调用同一个库的 `ournotes_free_string`；空指针会转换为计算错误。Rust 的 panic 应在 FFI 边界转换为 JSON 错误，避免跨越 C ABI。实现需支持并发调用，因为切换时旧任务可能继续执行。

- `dataset_info` 输出 `{ "datasetId": "<sha256>", "modelCommit": "<commit>" }`，数据身份与原网页请求保持一致。
- `capabilities` 使用当前 `ournotes-engine` 能力格式。
- `recommend_account` 使用原网页传入的 account/request JSON，返回 `ournotes-deck.account-recommendation/1` Answer。搜索预算、约束和「已证明最优」语义保持一致。
- 接口错误可返回 `{ "error": "说明" }`；Go 在加载前检查导出符号，检查能力结果，校验缓存库文件的 SHA-256。

参考实现：ournotes-deck 仓库的 `crates/ournotes-ffi/src/lib.rs`。

构建多线程实现后，将产物复制到源码根目录 `libournotes_ffi_experimental.dylib`，运行 `bun run build` 自动随 app 打包。也可通过 `OURNOTES_EXPERIMENTAL_FFI_LIB=/absolute/path/lib.dylib bun run build` 指定构建产物。也可放置在应用数据目录的 `engines/experimental.dylib`。在原生设置的「高级」中选择「实验性替换算法」即立即生效。下次启动恢复选择；「默认算法」切回内置版本。自定义库作为原生代码在应用进程中运行。

## LUCK desktop fallback (bdon 2026-10-10)

The default selection delegates ordinary scenes to bdon's original Worker. When the verified master music row contains a LUCK mission and the request plays Gekisou (Battle/Mission/Arena), the desktop routes the unchanged account/request JSON to its bundled native engine. Free Live, Challenge Live, Skip and Power continue through the original Worker. Experimental mode uses the selected native engine for every scene.

The current page rejects every `catalog.musics[].hasLuck === true` before submitting a request, regardless of engine capabilities. After native preparation succeeds and its capabilities declare `luckMissions: true`, the desktop sends a UI compatibility catalogue with those supported music rows marked `hasLuck: false`. The factual catalogue remains private to the bridge for routing; master mission values and chart bytes supplied to both solvers remain intact. Unknown mission metadata stays unavailable. Native initialization failure leaves the original catalogue and official LUCK restriction in effect.

The catalogue also carries the upstream per-event item-reward availability fields. Native jobs share the existing progress polling and cancellation path, and retain explicit unproven results when the time limit ends. Future bdon support gating should replace this compatibility adapter with a capability-aware check.
