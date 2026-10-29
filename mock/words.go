package mock

// The vocabulary the writer builds answers from. Plain and generic on purpose: answers read like
// prose without pretending to know anything.

var nouns = []string{
	"approach", "budget", "cache", "change", "client", "config", "context", "cost", "dataset",
	"default", "deadline", "decision", "dependency", "design", "detail", "draft", "edge case",
	"estimate", "example", "factor", "feature", "file", "goal", "guide", "habit", "input",
	"interface", "issue", "layer", "limit", "log", "loop", "metric", "method", "model", "module",
	"network", "option", "outcome", "output", "pattern", "plan", "policy", "process", "project",
	"queue", "request", "resource", "result", "risk", "rule", "schedule", "server", "service",
	"setting", "signal", "source", "step", "strategy", "system", "task", "team", "test", "timeline",
	"tool", "trade-off", "update", "user", "value", "version", "workflow", "workload",
}

var adjectives = []string{
	"simple", "clear", "stable", "small", "fast", "careful", "consistent", "flexible", "reliable",
	"explicit", "focused", "practical", "predictable", "robust", "shared", "steady", "useful",
	"narrow", "broad", "common", "safe", "quiet", "obvious", "short", "separate", "local",
	"manual", "automatic", "gradual", "familiar", "independent", "readable", "lightweight",
}

// verbs as [base, third person].
var verbs = [][2]string{
	{"adjust", "adjusts"}, {"build", "builds"}, {"check", "checks"}, {"clarify", "clarifies"},
	{"compare", "compares"}, {"cover", "covers"}, {"define", "defines"}, {"document", "documents"},
	{"handle", "handles"}, {"improve", "improves"}, {"keep", "keeps"}, {"limit", "limits"},
	{"measure", "measures"}, {"monitor", "monitors"}, {"organize", "organizes"},
	{"prioritize", "prioritizes"}, {"reduce", "reduces"}, {"review", "reviews"}, {"run", "runs"},
	{"shape", "shapes"}, {"simplify", "simplifies"}, {"split", "splits"}, {"support", "supports"},
	{"test", "tests"}, {"track", "tracks"}, {"update", "updates"}, {"validate", "validates"},
	{"affect", "affects"}, {"replace", "replaces"}, {"structure", "structures"},
}

var adverbs = []string{
	"Usually", "In practice", "Fortunately", "As a rule", "Even so", "In short", "Often",
	"In many cases", "Interestingly", "On the other hand", "Generally", "Over time",
}

var stopwords = map[string]bool{
	"about": true, "above": true, "after": true, "again": true, "also": true, "always": true,
	"another": true, "anything": true, "because": true, "been": true, "before": true, "being": true,
	"below": true, "between": true, "both": true, "could": true, "does": true, "doing": true,
	"down": true, "during": true, "each": true, "else": true, "even": true, "every": true,
	"explain": true, "from": true, "give": true, "have": true, "having": true, "here": true,
	"into": true, "just": true, "know": true, "like": true, "make": true, "many": true, "more": true,
	"most": true, "much": true, "must": true, "need": true, "only": true, "other": true, "over": true,
	"please": true, "really": true, "same": true, "should": true, "show": true, "some": true,
	"something": true, "such": true, "tell": true, "than": true, "that": true, "their": true,
	"them": true, "then": true, "there": true, "these": true, "they": true, "thing": true,
	"things": true, "think": true, "this": true, "those": true, "through": true, "under": true,
	"until": true, "very": true, "want": true, "what": true, "when": true, "where": true,
	"which": true, "while": true, "will": true, "with": true, "would": true, "write": true,
	"your": true, "yours": true, "able": true, "using": true, "used": true, "use": true,
	"how": true, "why": true, "who": true, "can": true, "the": true, "and": true, "for": true,
	"you": true, "are": true, "was": true, "were": true, "its": true, "it's": true, "i'm": true,
	"don't": true, "let's": true, "what's": true, "where's": true, "who's": true, "how's": true,
	"that's": true, "there's": true, "here's": true, "isn't": true, "aren't": true, "can't": true,
	"won't": true, "doesn't": true, "didn't": true, "i've": true, "i'd": true, "you're": true, "brief": true, "briefly": true, "short": true, "sentence": true,
	"word": true, "words": true, "answer": true, "reply": true, "respond": true, "question": true,
	"list": true, "steps": true, "keep": true, "alive": true, "good": true, "best": true,
	"better": true, "work": true, "works": true, "help": true, "start": true, "stop": true,
	"take": true, "look": true, "create": true, "describe": true, "possible": true,
	"different": true, "today": true, "tomorrow": true, "now": true, "currently": true,
	"getting": true, "going": true, "thanks": true, "thank": true, "okay": true, "sure": true,
	"compare": true, "comparison": true, "versus": true, "summarize": true, "summary": true,
	"once": true, "upon": true, "book": true, "find": true, "send": true, "check": true,
	"function": true, "script": true, "code": true, "snippet": true, "program": true,
	"implement": true, "not": true, "but": true, "all": true, "any": true, "one": true,
	"two": true, "get": true, "got": true, "has": true, "had": true, "may": true, "our": true,
	"out": true, "own": true, "say": true, "see": true, "she": true, "his": true, "her": true,
	"him": true, "let": true, "new": true, "old": true, "way": true, "too": true, "yes": true,
	"did": true, "off": true, "per": true, "via": true, "etc": true, "tips": true, "ideas": true,
}

var sentenceTemplates = []string{
	"The {t} {vs} the {n} by keeping each {n2} {a}.",
	"In most cases, the {t} works best when the {n} stays {a}.",
	"A {a} {n} makes it easier to {v} the {t} without an extra {n2}.",
	"You can {v} the {n} first, then {v2} the {t} once the {n2} is ready.",
	"This matters because a {a} {t} tends to {v} every {n} it touches.",
	"For example, a team might {v} the {t} and {v2} the {n} on a {a} schedule.",
	"{Adv}, the {n} {vs} faster than a {a} {n2}.",
	"The trade-off is that the {n} becomes less {a} as the {t} grows.",
	"It helps to think of the {t} as a {a} {n} rather than a {n2}.",
	"Keep the {n} {a} and the {n2} {a2}; that is usually enough.",
	"If the {n} {vs} too often, {v} the {n2} before anything else.",
	"Most people {v} the {t} once and then rarely {v2} it again.",
	"The {a} part is the {n}, which {vs} the {t} directly.",
	"That way the {n} never has to {v} the {n2} twice.",
	"There are a few {a} ways to {v} the {t}, and each one {vs} a different {n}.",
	"When in doubt, start with the {n} and measure the {n2}.",
	"Over time, the {t} {vs} a {a} {n} that is hard to {v} later.",
	"Note that the {n} does not {v} the {t} on its own.",
	"{Adv}, nothing about the {t} requires a {a} {n}.",
	"The result is a {a} {n} that {vs} the {n2} with little effort.",
	"A good first step is to {v} the {n} and write down what the {t} needs.",
	"Without a {a} {n}, the {t} can {v} the {n2} in ways that are hard to see.",
	"The {t} and the {n} are related, but they {v} different things.",
	"{Adv}, a {a} {n} is easier to {v} than a {a2} one.",
	"Treat the {n} as part of the {t}, not as an afterthought.",
	"Small changes to the {n} can {v} the {t} more than you expect.",
}

var stepTemplates = []string{
	"{V} the {n} and check that the {n2} is {a}.",
	"{V} the {t} with a {a} {n}.",
	"Make sure the {n} is {a} before you {v} the {n2}.",
	"Review the {n} and {v} anything that looks {a}.",
	"Repeat until the {t} {vs} the {n} the way you want.",
	"Write down the {n} so the {t} stays {a}.",
	"{V} one {n} at a time and keep the rest {a}.",
	"Ask whether the {n} still {vs} the {t}.",
	"Start small: {v} a single {n} first.",
	"Set a {a} {n} for the {t} and stick to it.",
}

var openers = []string{
	"Sure! Here's an overview of {t}.",
	"Here's how I'd think about {t}.",
	"Great question.",
	"Short answer: it depends on the {n}.",
	"Let's break {t} down.",
	"Happy to help with {t}.",
	"Good question, and a common one.",
	"",
	"",
}

var closers = []string{
	"Let me know if you want more detail on the {n}.",
	"Hope this helps!",
	"Want me to go deeper on any of these?",
	"If you share more about your {n}, I can be more specific.",
	"",
	"",
	"",
}

var headings = []string{
	"{T} at a glance", "Why the {n} matters", "Getting started", "Key points", "How it works",
	"Common pitfalls", "A {a} approach", "Summary", "What to watch for", "Next steps",
}

var greetings = []string{
	"Hello! How can I help you today?",
	"Hi there! What can I do for you?",
	"Hey! What would you like to talk about?",
	"Hello! What's on your mind?",
	"Hi! How can I assist you today?",
}

var thoughtTemplates = []string{
	"Okay, the user is asking about {t}.",
	"Let me think about what matters most here.",
	"First, the {n} probably {vs} the {t}.",
	"Wait, I should also consider the {n2}.",
	"Hmm, a {a} {n} might be the simpler way to explain it.",
	"So the answer should mention the {n} and the {t}.",
	"I'll keep it {a} and clear.",
	"Maybe an example with a {n} would help.",
	"Let me double-check: does the {n} really {v} the {t}? Yes, mostly.",
	"I don't need to cover every {n}, just the {a} ones.",
	"The user might also care about the {n2}.",
	"Alright, I think I have enough to answer.",
}

var imageTemplates = []string{
	"The image shows a {a} {n} in the foreground, with a {n2} behind it.",
	"The lighting is soft, and the colors are mostly muted.",
	"There appears to be some text near the edge, but it is too small to read.",
	"In the background, a {a} {n} takes up most of the frame.",
	"Overall, it looks like a {a} scene, probably taken indoors.",
	"Nothing in the picture looks unusual or out of place.",
}

var codeLanguages = map[string]string{
	"python": "python", "py": "python", "django": "python", "pandas": "python",
	"javascript": "javascript", "js": "javascript", "node": "javascript", "react": "javascript",
	"typescript": "typescript", "ts": "typescript",
	"go": "go", "golang": "go",
	"rust": "rust",
	"bash": "bash", "shell": "bash", "sh": "bash", "zsh": "bash",
	"sql": "sql", "postgres": "sql", "mysql": "sql", "sqlite": "sql", "query": "sql",
	"ruby": "ruby", "rails": "ruby",
	"swift": "swift",
}
