#!/usr/bin/env python3
"""
Generate test polls using notable GitHub users.

Usage:
    python scripts/generate-test-polls.py --count 5
    python scripts/generate-test-polls.py --category language_creators
"""

import json
import random
import argparse
from pathlib import Path


def load_users(data_file="data/notable-github-users.json"):
    """Load notable GitHub users from JSON file."""
    with open(data_file) as f:
        return json.load(f)


def generate_poll_request(users_data, category=None, num_options=2):
    """Generate a random poll request."""
    # Select random category if not specified
    categories = ["language_creators", "framework_authors", "tool_creators", "oss_legends"]
    if category and category in users_data:
        selected_category = category
    else:
        selected_category = random.choice(categories)

    # Get users from that category
    users = users_data[selected_category]
    if len(users) < num_options:
        num_options = len(users)

    # Select random users
    selected_users = random.sample(users, num_options)
    usernames = [f"@{u['username']}" for u in selected_users]

    # Select appropriate template based on number of options
    templates = users_data["poll_templates"]
    if num_options == 2:
        two_option_templates = [t for t in templates if "{user3}" not in t]
        template = random.choice(two_option_templates)
    else:
        template = random.choice(templates)

    # Format the template
    category_name = selected_category.replace("_", " ")
    if num_options == 2:
        poll_request = template.format(
            category=category_name,
            user1=usernames[0],
            user2=usernames[1]
        )
    else:
        poll_request = template.format(
            category=category_name,
            user1=usernames[0],
            user2=usernames[1],
            user3=usernames[2] if len(usernames) > 2 else usernames[0]
        )

    return poll_request.strip()


def main():
    parser = argparse.ArgumentParser(description="Generate test poll requests")
    parser.add_argument("--count", type=int, default=5, help="Number of polls to generate")
    parser.add_argument("--category", type=str, help="Specific category to use")
    parser.add_argument("--options", type=int, default=2, help="Number of options per poll (2-3)")

    args = parser.parse_args()

    users_data = load_users()

    print(f"🎲 Generating {args.count} random poll requests:\n")

    for i in range(args.count):
        poll_request = generate_poll_request(users_data, args.category, args.options)
        print(f"{i+1}. {poll_request}")

    print(f"\n✅ Generated {args.count} poll requests!")
    print("\nCopy any of these to test your poll creation feature!")


if __name__ == "__main__":
    main()
