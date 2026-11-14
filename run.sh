#!/usr/bin/env bash
set -eo pipefail

CLR_RESET="\033[0m"
CLR_CYAN="\033[1;36m"
CLR_GREEN="\033[1;32m"
CLR_RED="\033[1;31m"
CLR_YELLOW="\033[1;33m"
CLR_BLUE="\033[1;34m"
CLR_WHITE="\033[1;37m"

print_step() {
    printf "${CLR_CYAN}[*]${CLR_RESET} %s\n" "$1"
}

print_success() {
    printf "${CLR_GREEN}[✓]${CLR_RESET} %s\n" "$1"
}

print_warning() {
    printf "${CLR_YELLOW}[!][WARNING]${CLR_RESET} %s\n" "$1"
}

print_error() {
    printf "${CLR_RED}[!] %s${CLR_RESET}\n" "$1"
}

print_item() {
    local key="$1"
    local val="$2"
    local val_color="${3:-$CLR_WHITE}"
    printf "    • ${CLR_WHITE}%-26s${CLR_RESET}: ${val_color}%s${CLR_RESET}\n" "$key" "$val"
}

# 1. 拉取远程最新代码
print_step "正在拉取远程代码仓库 (git pull --ff-only origin main)..."
if git pull --ff-only origin main; then
    CURRENT_COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")
    COMMIT_MSG=$(git log -1 --pretty=%s 2>/dev/null || echo "")
    print_success "代码拉取成功"
    print_item "当前分支" "$(git rev-parse --abbrev-ref HEAD 2>/dev/null || echo 'main')" "$CLR_BLUE"
    print_item "最新提交" "${CURRENT_COMMIT} (${COMMIT_MSG})" "$CLR_BLUE"
else
    print_error "Git 拉取失败！可能存在工作区修改冲突或分支分叉，终止部署"
    exit 1
fi

# 2. 同步基础配置文件与模板
print_step "同步配置文件与环境变量模板..."
if [ ! -f config.yaml ]; then
    cp config.example.yaml config.yaml
fi
if [ ! -f .env ]; then
    cp .env.example .env
fi
cp deploy/nginx.conf.example deploy/nginx.conf
print_success "基础配置文件检查完成"

# 2.1 注入生产环境变量 (优先加载 /home/gha_deploy/.pam_environment)
PAM_ENV_FILE="/home/gha_deploy/.pam_environment"
if [ -f "$PAM_ENV_FILE" ]; then
    print_step "检测到生产环境变量文件 (${PAM_ENV_FILE})，正在加载并同步至 .env..."
    while IFS= read -r line || [ -n "$line" ]; do
        [[ "$line" =~ ^[[:space:]]*# ]] && continue
        [[ -z "${line// }" ]] && continue
        var_name=$(echo "$line" | awk '{print $1}')
        if [[ "$line" =~ OVERRIDE=\"([^\"]*)\" ]]; then
            val="${BASH_REMATCH[1]}"
        elif [[ "$line" =~ DEFAULT=\"([^\"]*)\" ]]; then
            val="${BASH_REMATCH[1]}"
        else
            continue
        fi
        export "$var_name=$val"
        if grep -q "^${var_name}=" .env 2>/dev/null; then
            sed -i "s|^${var_name}=.*|${var_name}=${val}|" .env
        else
            echo "${var_name}=${val}" >> .env
        fi
    done < "$PAM_ENV_FILE"
    print_success "生产环境变量已成功加载并写入 .env"
    print_item "运行模式" "读写模式 (READ_ONLY=${READ_ONLY:-false})" "$CLR_GREEN"
    print_item "当前库连接" "${APP_DB_DSN:-未显式设置}" "$CLR_BLUE"
else
    # 若无 pam_environment，确保 .env 中 READ_ONLY 为 false，库指向 flarum
    if grep -q "^READ_ONLY=true" .env 2>/dev/null; then
        sed -i 's/^READ_ONLY=true/READ_ONLY=false/' .env
    fi
    if grep -q "mysql:3306)/bbs" .env 2>/dev/null; then
        sed -i 's|mysql:3306)/bbs|mysql:3306)/flarum|' .env
    fi
fi

# 3. 检查并同步 Nginx 配置
print_step "检查 Nginx 配置变动..."
NGINX_TARGET="/etc/nginx/sites-available/bbs.jnsec.org"
NGINX_ENABLED="/etc/nginx/sites-enabled/bbs.jnsec.org"

if [ ! -f "$NGINX_TARGET" ] || ! diff -q deploy/nginx.conf "$NGINX_TARGET" >/dev/null 2>&1; then
    print_step "检测到 Nginx 配置变更，准备同步并重新加载..."
    if sudo cp deploy/nginx.conf "$NGINX_TARGET"; then
        print_success "已覆盖 Nginx 目标配置 -> ${NGINX_TARGET}"
        
        print_step "测试 Nginx 配置语法 (sudo nginx -t)..."
        if sudo nginx -t; then
            print_success "Nginx 语法测试通过"
            print_step "重新加载 Nginx 服务 (sudo nginx -s reload)..."
            if sudo nginx -s reload; then
                print_success "Nginx 服务重载完成"
            else
                print_error "Nginx 重载失败！"
                exit 1
            fi
        else
            print_error "Nginx 语法测试未通过，已中止重载！"
            exit 1
        fi
    else
        print_error "复制 Nginx 配置失败 (sudo cp 权限异常)！"
        exit 1
    fi
else
    print_success "Nginx 配置无变动，无需重载"
    print_item "对比状态" "deploy/nginx.conf 与目标配置一致" "$CLR_GREEN"
fi

# 4. 检查生效的活动链接是否指向目标文件
if [ -L "$NGINX_ENABLED" ]; then
    ACTIVE_TARGET=$(readlink -f "$NGINX_ENABLED" 2>/dev/null || true)
    RAW_TARGET=$(readlink "$NGINX_ENABLED" 2>/dev/null || true)

    if [ ! -e "$NGINX_ENABLED" ]; then
        print_error "软链接失效！${NGINX_ENABLED} 指向的源文件 (${RAW_TARGET}) 已不存在！"
        print_warning "请在服务器执行: sudo ln -sf ${NGINX_TARGET} ${NGINX_ENABLED}"
    elif [ "$ACTIVE_TARGET" = "$NGINX_TARGET" ]; then
        print_success "活动软链接已正确生效 -> ${NGINX_TARGET}"
    else
        print_warning "注意：${NGINX_ENABLED} 当前指向 ${ACTIVE_TARGET}，并非当前更新的 ${NGINX_TARGET}！"
    fi
elif [ -f "$NGINX_ENABLED" ]; then
    print_item "生效配置类型" "独立文件 (非软链接)" "$CLR_YELLOW"
elif [ ! -e "$NGINX_ENABLED" ]; then
    print_warning "未找到 ${NGINX_ENABLED}，请在服务器执行: sudo ln -sf ${NGINX_TARGET} ${NGINX_ENABLED}"
fi

# 5. 构建并启动应用容器
print_step "开始构建应用镜像 (docker-compose build app)..."
docker-compose build app
print_success "应用镜像构建完成"

# 5.1 数据库迁移先于应用启动：应用对未迁移库 Fail-Fast 拒绝启动，绝不能先起容器再补迁移。
# DDL 需要 root 权限（bbs_go_shared 是最小权限运行账号），迁移容器临时注入 root DSN。
if [ -z "$MYSQL_ROOT_PASSWORD" ]; then
    MYSQL_ROOT_PASSWORD=$(grep '^MYSQL_ROOT_PASSWORD=' .env | head -1 | cut -d= -f2-)
fi
if [ -n "$MYSQL_ROOT_PASSWORD" ]; then
    print_step "应用数据库迁移 (一次性容器, root DDL 权限)..."
    docker-compose run --rm \
        -e "JNSEC_DB_DSN=root:${MYSQL_ROOT_PASSWORD}@tcp(mysql:3306)/flarum?charset=utf8mb4&parseTime=True&loc=Local" \
        app migrate-db
else
    print_warning "缺少 MYSQL_ROOT_PASSWORD，使用应用账号执行迁移（新 DDL 迁移将失败）..."
    docker-compose run --rm app migrate-db
fi
print_success "数据库迁移完成"

print_step "启动应用容器 (docker-compose up -d --no-deps app)..."
docker-compose up -d --no-deps app
print_success "容器启动完成"

print_step "检查容器运行状态 (bbs-app)..."
docker ps -a --filter name=bbs-app

# 6. 清理悬空镜像
print_step "清理悬空 Docker 镜像 (docker image prune -f)..."
docker image prune -f
print_success "部署流程全部执行完成！"
