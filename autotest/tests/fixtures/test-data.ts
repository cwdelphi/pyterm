// 测试数据工厂
export const TEST_CONFIG = {
  baseUrl: process.env.BASE_URL || 'https://127.0.0.1:5588',
  admin: { username: 'admin', password: 'change_me_pass' },

  // 本地 Agent
  localAgent: {
    id: 'local-agent',
    name: '本地连接',
  },

  // 远程 Agent
  remoteAgent: {
    id: 'remote-agent',
    name: 'remote-agent',
  },

  // 本地网关
  localGateway: {
    id: 'local-gateway',
    name: '本地网关',
  },

  // 测试 SSH 服务器 (test-ssh-server 容器)
  testSSH: {
    host: '127.0.0.1',
    port: 2222,
    username: 'sshuser',
    password: 'change_me_sshpass',
  },
}

export interface TestConnection {
  name: string
  host: string
  port: number
  username: string
  password: string
  agentId: string
  gatewayId?: string
}

// 生成测试连接名称 (带唯一前缀)
export function genConnName(prefix: string, suffix: string): string {
  return `${prefix}${suffix}`
}

// 场景 A: 直连 + 本地 Agent
export function makeConnA(prefix: string): TestConnection {
  return {
    name: genConnName(prefix, 'local_agent_ssh'),
    host: TEST_CONFIG.testSSH.host,
    port: TEST_CONFIG.testSSH.port,
    username: TEST_CONFIG.testSSH.username,
    password: TEST_CONFIG.testSSH.password,
    agentId: TEST_CONFIG.localAgent.id,
  }
}

// 场景 B: 直连 + 远程 Agent
export function makeConnB(prefix: string): TestConnection {
  return {
    name: genConnName(prefix, 'remote_agent_ssh'),
    host: TEST_CONFIG.testSSH.host,
    port: TEST_CONFIG.testSSH.port,
    username: TEST_CONFIG.testSSH.username,
    password: TEST_CONFIG.testSSH.password,
    agentId: TEST_CONFIG.remoteAgent.id,
  }
}

// 场景 C: 本地网关 + 本地 Agent
export function makeConnC(prefix: string): TestConnection {
  return {
    name: genConnName(prefix, 'gw_local_agent_ssh'),
    host: TEST_CONFIG.testSSH.host,
    port: TEST_CONFIG.testSSH.port,
    username: TEST_CONFIG.testSSH.username,
    password: TEST_CONFIG.testSSH.password,
    agentId: TEST_CONFIG.localAgent.id,
    gatewayId: TEST_CONFIG.localGateway.id,
  }
}

// 场景 D: 本地网关 + 远程 Agent
export function makeConnD(prefix: string): TestConnection {
  return {
    name: genConnName(prefix, 'gw_remote_agent_ssh'),
    host: TEST_CONFIG.testSSH.host,
    port: TEST_CONFIG.testSSH.port,
    username: TEST_CONFIG.testSSH.username,
    password: TEST_CONFIG.testSSH.password,
    agentId: TEST_CONFIG.remoteAgent.id,
    gatewayId: TEST_CONFIG.localGateway.id,
  }
}
