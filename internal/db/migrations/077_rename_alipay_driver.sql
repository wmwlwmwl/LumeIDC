-- 支付宝驱动标识符由 alipay_f2f 更名为 alipay。
-- 该驱动现已同时支持当面付预下单扫码与电脑/手机网站支付跳转，旧名（face-to-face）
-- 不再准确，且与 epay/mock 的命名风格不一致。
-- gateways.driver 是持久化标识符，存量网关实例必须同步更新，否则驱动查不到会全部停收。
UPDATE gateways SET driver='alipay' WHERE driver='alipay_f2f';
