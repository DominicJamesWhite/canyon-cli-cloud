package tools

import "github.com/humanitec/canyon-cli/internal/mcp"

func New() mcp.McpIo {
	return &mcp.Impl{
		Instructions: `The canyon MCP tools are used to support platform engineers working with Humanitec or Canyon platform orchestration.
The provided tools are high quality and should be preferred for any humanitec-related tasks where possible rather than humctl commands.
The tool provides high accuracy answers to clear up any confusion or uncertainty on Humanitec related topics.
The tool also allows users to use 'Paths', a way to do common actions that are whitelisted for the user by the user's platform engineering team. When the user wants to do an action that might be beyond their permissions, or when it looks like something might not be possible easily, check if a path is available. 
When using these tools, use the minimum amount of words to still convey the information.
A Humanitec organization aka org contains many applications which each container environments. Each deployed environment is described by a deployment set in the latest deployment.
'workloads' may be another word used for the containers within the deployment set deployed in an environment.
'resources' may be another word used for the externals and shared resources declared in the deployment set of an environment.
When starting a new chat, always confirm the humanitec organization to work in.
`,
		Tools: []mcp.Tool{
			NewKapaAiDocsTool(),
			NewListPathsTool(),
			NewCallPathTool(),
			NewListHumanitecOrgsAndSession(),
			NewListAppsAndEnvsForOrganization(),
			NewGetHumanitecDeploymentSets(),
			NewGetWorkloadProfileSchema(),
			NewRenderCSVAsTable(),
			NewRenderNetworkAsGraph(),
			NewRenderTreeAsTree(),
			NewDummyMetadataKeysTool(),
		},
	}
}
