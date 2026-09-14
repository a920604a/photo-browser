# Resource measurements

> 尚未在目標 NAS 上執行。以下只有「怎麼跑」與「跑完要做什麼」。

在 NAS 上執行（**離峰時段**，因為第二段會重建所有縮圖）：

```
bash scripts/measure-resources.sh \
  --compose deploy/compose/docker-compose.prod.yml \
  --out docs/deploy/resource-measurements.md \
  --idle-seconds 120
```

跑完後：

1. 把產生的內容 commit 進來。
2. 依 "During index --rebuild-thumbnails" 的 peak RSS 調整
   `deploy/compose/.env.prod` 的 `APP_MEM_LIMIT`（建議取 peak 的 1.5 倍，
   但三個 service 的總和必須明顯低於 2048 MiB，DSM 本身還要數百 MiB）。
3. 用最大的一張代表性照片單獨驗證 decode 不會 OOM。若被 OOM kill（exit 137），
   **調高 limit，不要降低圖片品質**。
4. 在 `docs/deploy/03-acceptance.md` 勾掉對應項目。

## 開發機參考值（非驗收依據）

在 Apple Silicon Mac 上、以 22 張照片的 fixture library 對 prodcheck stack 量測：

| 階段 | photo-app（serve） | photo-app（index run） | nginx | 總計 peak RSS |
|---|---|---|---|---|
| Idle | 43 MiB | — | 3 MiB | 89 MiB |
| `index --rebuild-thumbnails` | 43 MiB | **64 MiB / 102.9% CPU** | 3 MiB | 153 MiB |

索引容器的 CPU 停在單核滿載附近，與 thumbnail concurrency 固定為 1 的設計一致。
Braswell 單核比這台機器慢得多，所以 NAS 上的**時間**會長很多，但**記憶體**足跡
應該落在同一個量級——這正是要在 NAS 上實測確認的事。
