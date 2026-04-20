# 分支工作流说明

## 远程仓库

| 名称 | URL | 用途 |
|------|-----|------|
| `origin` | `https://github.com/QuantumNous/new-api.git` | 上游 new-api 原始仓库（只拉取，不推送） |
| `custom` | `git@github.com:zehuiya/custom-newapi.git` | 自定义仓库（推送定制化代码） |

## 分支

| 分支 | 用途 |
|------|------|
| `main` | 与上游 new-api 保持一致，**不做任何定制化修改** |
| `onesapi` | 定制化开发分支，包含所有自定义功能 |

## 常用操作

### 拉取上游最新代码并合并到 onesapi

```bash
# 1. 切到 main，拉取上游最新
git checkout main
git pull origin main

# 2. 推送最新的 main 到 custom 仓库
git push custom main

# 3. 切到 onesapi，合并最新 main
git checkout onesapi
git merge main

# 4. 解决冲突（如有），然后推送
git push custom onesapi
```

### 在 onesapi 上开发新功能

```bash
# 确保在 onesapi 分支
git checkout onesapi

# 开发 & 提交
git add .
git commit -m "feat: your feature description"

# 推送到 custom 仓库
git push custom onesapi
```

### 克隆并设置（新环境）

```bash
# 克隆 custom 仓库
git clone git@github.com:zehuiya/custom-newapi.git
cd custom-newapi

# 添加上游 origin
git remote add origin https://github.com/QuantumNous/new-api.git

# 拉取上游分支信息
git fetch origin

# 切到 onesapi 开发分支
git checkout onesapi
```

## 注意事项

- **永远不要在 main 分支上直接修改代码**，main 仅用于同步上游
- 所有定制化功能都在 `onesapi` 分支上开发
- 定期合并 main 到 onesapi 以保持与上游同步
- 合并时如遇冲突，以 onesapi 的定制化代码为准
