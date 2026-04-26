/** localStorage：用户点击「今日关闭」后，当天不再自动弹出系统消息（若布局层接入校验）。 */
export const MESSAGE_CENTER_DISMISS_YMD_KEY = 'lingvoice.msgCenter.dismissYmd'

export function getTodayYmd(): string {
  return new Date().toISOString().slice(0, 10)
}

export function dismissMessageCenterForToday(): void {
  try {
    localStorage.setItem(MESSAGE_CENTER_DISMISS_YMD_KEY, getTodayYmd())
  } catch {
    /* ignore */
  }
}

export function isMessageCenterDismissedToday(): boolean {
  try {
    return localStorage.getItem(MESSAGE_CENTER_DISMISS_YMD_KEY) === getTodayYmd()
  } catch {
    return false
  }
}
