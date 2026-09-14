# NAS preflight report

> 尚未在目標 NAS 上執行。

執行方式（在 NAS 上，經 SSH）：

```
git clone <repo> && cd photo-browser
make test                     # 先確認 image 能在 NAS 上 build
bash scripts/nas-preflight.sh \
  --photo-root /volume1/photos \
  --data-dir   /volume1/docker/photo-browser/data \
  --thumb-dir  /volume1/docker/photo-browser/thumbnails \
  --out docs/deploy/preflight-report.md
```

跑完後把產生的內容 commit 進來，並在 `docs/deploy/03-acceptance.md` 勾掉對應項目。
若腳本以 exit 1 結束，**先解決 BLOCKING 項目再往下做**——後面每個 task 都假設這些前提成立。

## 已知：HEIC

在開發機的 `photo-browser-test` 映像上，libvips 8.14.1 **有** `heifload`
（probe 回報 4 個相關 operation）。這**不代表** HEIC 進入 POC scope：

- `internal/scanner` 的 `supportedExt` 只收 `.jpg/.jpeg/.png/.webp`，HEIC 檔案會被
  標記為 `unsupported` warning 並略過。
- Spec §3 要求 HEIC 必須另做 memory probe 才能納入；在 2 GB 的 DS716+II 上，
  一張全尺寸 HEIC 的解碼記憶體足跡尚未量測。

要改變這個結論，需要一份獨立的 follow-up spec，不是改一行副檔名清單。
