/** 将可识别的设备和镜像错误转换为操作提示；未知错误保留原文。 */
export function formatDeviceError(error: unknown): string {
    const message = error instanceof Error ? error.message : String(error ?? '')
    const lower = message.toLowerCase()

    if (lower.includes('devicelocked') || lower.includes('device is locked')) {
        return '设备已锁定，请解锁 iPhone 或 iPad 后重试。'
    }
    if (lower.includes('pairingdialogresponsepending')) {
        return '设备正在等待配对确认，请解锁设备并在弹出的提示中选择“信任”。'
    }
    if (lower.includes('userdeniedpairing')) {
        return '设备拒绝了配对请求，请重新连接设备并选择“信任此电脑”。'
    }
    if (
        lower.includes('invalidhostid') ||
        lower.includes('invalid host id') ||
        lower.includes('invalidpairrecord')
    ) {
        return '设备配对信息无效，请解锁设备、重新连接 USB 并选择“信任此电脑”。'
    }
    if (lower.includes('developermodedisabled')) {
        return '设备未启用开发者模式，请在“设置 → 隐私与安全性 → 开发者模式”中启用后重试。'
    }
    if (
        lower.includes('failed to find identity for apboardid') ||
        lower.includes('could not find identity for identifiers')
    ) {
        return '开发者镜像中没有找到与当前设备匹配的信息，请检查镜像版本后重试。'
    }
    if (lower.includes('unexpected status in response 94')) {
        return 'Apple 签名服务拒绝了开发者镜像请求，请检查设备和镜像是否匹配后重试；详情请查看系统日志。'
    }
    if (lower.includes('x509:') || lower.includes('certificate verify failed')) {
        return '网络证书验证失败，请检查电脑日期、系统证书及网络代理后重试。'
    }
    if (lower.includes('no such host')) {
        return '无法解析服务器地址，请检查网络连接、DNS 或代理设置后重试。'
    }
    if (lower.includes('context deadline exceeded') || lower.includes('i/o timeout')) {
        return '操作超时，请检查设备连接或网络状态后重试。'
    }
    if (
        lower.includes('connection reset by peer') ||
        lower.includes('broken pipe') ||
        /(?:^|[:\s])eof\s*$/i.test(message)
    ) {
        return '连接在操作过程中提前关闭，请检查设备连接和网络后重试；详情请查看系统日志。'
    }
    if (lower.includes('no ios devices are attached') || /device '.+' not found/i.test(message)) {
        return '未找到设备，请检查 USB 连接、解锁设备并刷新列表。'
    }
    return message || '未知错误，请查看系统日志。'
}
